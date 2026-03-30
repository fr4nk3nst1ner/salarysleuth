package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const passwordHashIterations = 100000

type User struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by"`
}

type UserStoreData struct {
	Users map[string]User `json:"users"`
}

type RegistrationToken struct {
	Token     string    `json:"token"`
	Role      string    `json:"role"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
	UsedBy    string    `json:"used_by,omitempty"`
}

type TokenStoreData struct {
	Tokens map[string]RegistrationToken `json:"tokens"`
}

type Session struct {
	Username  string
	Role      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

var (
	userStoreMu  sync.RWMutex
	tokenStoreMu sync.RWMutex
	sessionMu    sync.RWMutex
	sessions     = make(map[string]*Session)
)

const sessionMaxAge = 24 * time.Hour
const sessionCookieName = "session"

// --- Password hashing (iterated SHA-256 with salt) ---

func hashPassword(password string) (string, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := deriveKey(password, salt)
	return hex.EncodeToString(salt) + "$" + hex.EncodeToString(dk), nil
}

func verifyPassword(password, stored string) bool {
	parts := strings.SplitN(stored, "$", 2)
	if len(parts) != 2 {
		return false
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	actual := deriveKey(password, salt)
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

func deriveKey(password string, salt []byte) []byte {
	input := make([]byte, len(salt)+len(password))
	copy(input, salt)
	copy(input[len(salt):], password)
	h := sha256.Sum256(input)
	for i := 1; i < passwordHashIterations; i++ {
		h = sha256.Sum256(h[:])
	}
	return h[:]
}

// --- User store ---

func userStoreFile() string { return filepath.Join(config.DataDir, "users.json") }

func loadUsers() *UserStoreData {
	userStoreMu.RLock()
	defer userStoreMu.RUnlock()
	return loadUsersUnsafe()
}

func loadUsersUnsafe() *UserStoreData {
	data, err := os.ReadFile(userStoreFile())
	if err != nil {
		return &UserStoreData{Users: make(map[string]User)}
	}
	var store UserStoreData
	if err := json.Unmarshal(data, &store); err != nil {
		return &UserStoreData{Users: make(map[string]User)}
	}
	if store.Users == nil {
		store.Users = make(map[string]User)
	}
	return &store
}

func saveUsersUnsafe(store *UserStoreData) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(userStoreFile(), data, 0600)
}

// --- Token store ---

func tokenStoreFile() string { return filepath.Join(config.DataDir, "tokens.json") }

func loadTokens() *TokenStoreData {
	tokenStoreMu.RLock()
	defer tokenStoreMu.RUnlock()
	return loadTokensUnsafe()
}

func loadTokensUnsafe() *TokenStoreData {
	data, err := os.ReadFile(tokenStoreFile())
	if err != nil {
		return &TokenStoreData{Tokens: make(map[string]RegistrationToken)}
	}
	var store TokenStoreData
	if err := json.Unmarshal(data, &store); err != nil {
		return &TokenStoreData{Tokens: make(map[string]RegistrationToken)}
	}
	if store.Tokens == nil {
		store.Tokens = make(map[string]RegistrationToken)
	}
	return &store
}

func saveTokensUnsafe(store *TokenStoreData) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(tokenStoreFile(), data, 0600)
}

// --- Session management ---

func createSession(username, role string) string {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)

	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessions[token] = &Session{
		Username:  username,
		Role:      role,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(sessionMaxAge),
	}
	return token
}

func getSession(token string) *Session {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	s, ok := sessions[token]
	if !ok || time.Now().After(s.ExpiresAt) {
		return nil
	}
	return s
}

func deleteSession(token string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	delete(sessions, token)
}

func cleanExpiredSessions() {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	now := time.Now()
	for k, s := range sessions {
		if now.After(s.ExpiresAt) {
			delete(sessions, k)
		}
	}
}

// --- Auth helpers ---

func isAuthEnabled() bool {
	store := loadUsers()
	return len(store.Users) > 0
}

func authenticateRequest(r *http.Request) *User {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && cookie.Value != "" {
		sess := getSession(cookie.Value)
		if sess != nil {
			store := loadUsers()
			if user, exists := store.Users[sess.Username]; exists {
				return &user
			}
		}
	}
	return nil
}

func isValidUsername(s string) bool {
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// --- Middleware ---

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthEnabled() {
			r.Header.Set("X-Auth-User", "anonymous")
			r.Header.Set("X-Auth-Role", "admin")
			next(w, r)
			return
		}
		user := authenticateRequest(r)
		if user == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
			return
		}
		r.Header.Set("X-Auth-User", user.Username)
		r.Header.Set("X-Auth-Role", user.Role)
		next(w, r)
	}
}

func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthEnabled() {
			r.Header.Set("X-Auth-User", "anonymous")
			r.Header.Set("X-Auth-Role", "admin")
			next(w, r)
			return
		}
		user := authenticateRequest(r)
		if user == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
			return
		}
		if user.Role != "admin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Admin access required"})
			return
		}
		r.Header.Set("X-Auth-User", user.Username)
		r.Header.Set("X-Auth-Role", user.Role)
		next(w, r)
	}
}

// --- Seed initial admin from env vars ---

func seedAdminUser() {
	userStoreMu.Lock()
	defer userStoreMu.Unlock()
	store := loadUsersUnsafe()
	if len(store.Users) > 0 {
		return
	}
	username := os.Getenv("WEB_USERNAME")
	password := os.Getenv("WEB_PASSWORD")
	if username == "" || password == "" {
		return
	}
	hash, err := hashPassword(password)
	if err != nil {
		log.Printf("Warning: failed to hash initial admin password: %v", err)
		return
	}
	store.Users[username] = User{
		Username:     username,
		PasswordHash: hash,
		Role:         "admin",
		CreatedAt:    time.Now(),
		CreatedBy:    "system",
	}
	if err := saveUsersUnsafe(store); err != nil {
		log.Printf("Warning: failed to save initial admin user: %v", err)
		return
	}
	log.Printf("Created initial admin user: %s", username)
}

func startSessionCleanup() {
	go func() {
		for {
			time.Sleep(15 * time.Minute)
			cleanExpiredSessions()
		}
	}()
}

// --- Handlers ---

func handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	if !isAuthEnabled() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"authenticated": true,
			"username":      "anonymous",
			"role":          "admin",
		})
		return
	}
	user := authenticateRequest(r)
	if user == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"authenticated": false,
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": true,
		"username":      user.Username,
		"role":          user.Role,
	})
}

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if user := authenticateRequest(r); user != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(loginPageHTML))
}

func handleLoginAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		jsonError(w, "Username and password are required", http.StatusBadRequest)
		return
	}

	store := loadUsers()
	user, exists := store.Users[req.Username]
	if !exists || !verifyPassword(req.Password, user.PasswordHash) {
		jsonError(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	token := createSession(user.Username, user.Role)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionMaxAge.Seconds()),
	})

	log.Printf("User logged in: %s", user.Username)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"username": user.Username,
		"role":     user.Role,
	})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		deleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

var loginPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Login | OffSec Jobs</title>
	<link rel="preconnect" href="https://fonts.googleapis.com">
	<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
	<link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;600;700&family=Outfit:wght@300;400;500;600;700&display=swap" rel="stylesheet">
	<style>
		:root {
			--bg-primary: #0a0a0f;
			--bg-secondary: #12121a;
			--bg-card: #16161f;
			--accent-primary: #00ff88;
			--accent-secondary: #00cc6a;
			--accent-glow: rgba(0, 255, 136, 0.15);
			--text-primary: #e8e8ed;
			--text-secondary: #8888a0;
			--text-muted: #5a5a70;
			--border-color: #2a2a3a;
			--danger: #ff4757;
		}
		* { margin: 0; padding: 0; box-sizing: border-box; }
		body {
			font-family: 'Outfit', sans-serif;
			background: var(--bg-primary);
			color: var(--text-primary);
			min-height: 100vh;
			display: flex;
			align-items: center;
			justify-content: center;
		}
		body::before {
			content: '';
			position: fixed;
			inset: 0;
			background:
				radial-gradient(ellipse at 20% 20%, rgba(0, 255, 136, 0.08) 0%, transparent 50%),
				radial-gradient(ellipse at 80% 80%, rgba(0, 204, 106, 0.06) 0%, transparent 50%);
			pointer-events: none;
			z-index: -1;
		}
		.login-container {
			width: 90%;
			max-width: 400px;
		}
		.logo {
			font-family: 'JetBrains Mono', monospace;
			font-size: 2rem;
			font-weight: 700;
			color: var(--accent-primary);
			text-shadow: 0 0 30px var(--accent-glow);
			text-align: center;
			margin-bottom: 0.5rem;
		}
		.logo span { color: var(--text-primary); }
		.tagline {
			color: var(--text-secondary);
			font-size: 0.9rem;
			text-align: center;
			margin-bottom: 2rem;
		}
		.login-card {
			background: var(--bg-secondary);
			border: 1px solid var(--border-color);
			border-radius: 14px;
			padding: 2rem;
		}
		.login-card h2 {
			font-size: 1.3rem;
			margin-bottom: 1.5rem;
			color: var(--text-primary);
		}
		.form-group {
			margin-bottom: 1.25rem;
		}
		.form-group label {
			display: block;
			font-size: 0.75rem;
			color: var(--text-muted);
			text-transform: uppercase;
			letter-spacing: 0.5px;
			margin-bottom: 0.4rem;
		}
		.form-group input {
			width: 100%;
			padding: 0.75rem 1rem;
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 8px;
			color: var(--text-primary);
			font-size: 1rem;
			font-family: 'Outfit', sans-serif;
			transition: border-color 0.2s;
		}
		.form-group input:focus {
			outline: none;
			border-color: var(--accent-primary);
			box-shadow: 0 0 10px var(--accent-glow);
		}
		.form-group input::placeholder { color: var(--text-muted); }
		.form-error {
			color: var(--danger);
			font-size: 0.85rem;
			margin-bottom: 1rem;
			padding: 0.6rem 0.8rem;
			background: rgba(255, 71, 87, 0.1);
			border-radius: 6px;
			display: none;
		}
		.btn-login {
			width: 100%;
			padding: 0.75rem;
			background: linear-gradient(135deg, var(--accent-primary), var(--accent-secondary));
			color: var(--bg-primary);
			border: none;
			border-radius: 8px;
			font-size: 1rem;
			font-weight: 600;
			cursor: pointer;
			font-family: 'Outfit', sans-serif;
			transition: all 0.2s;
		}
		.btn-login:hover { transform: translateY(-1px); box-shadow: 0 5px 15px var(--accent-glow); }
		.btn-login:disabled { opacity: 0.6; cursor: not-allowed; transform: none; box-shadow: none; }
		.login-footer {
			margin-top: 1.5rem;
			text-align: center;
			font-size: 0.85rem;
			color: var(--text-muted);
		}
		.login-footer a {
			color: var(--accent-primary);
			text-decoration: none;
			cursor: pointer;
		}
		.login-footer a:hover { text-decoration: underline; }
		.register-link { margin-top: 1rem; }
	</style>
</head>
<body>
	<div class="login-container">
		<h1 class="logo">Offsec<span>Jobs</span></h1>
		<p class="tagline">Sign in to access search and management features</p>
		<div class="login-card">
			<h2>Sign In</h2>
			<div id="login-error" class="form-error"></div>
			<form onsubmit="return doLogin(event)">
				<div class="form-group">
					<label for="username">Username</label>
					<input type="text" id="username" name="username" placeholder="Enter your username" autocomplete="username" autofocus>
				</div>
				<div class="form-group">
					<label for="password">Password</label>
					<input type="password" id="password" name="password" placeholder="Enter your password" autocomplete="current-password">
				</div>
				<button type="submit" class="btn-login" id="login-btn">Sign In</button>
			</form>
			<div class="login-footer register-link">
				Have a registration token? <a onclick="showRegSection()">Create an account</a>
			</div>
		</div>
		<div id="register-section" class="login-card" style="display:none;margin-top:1rem">
			<h2>Register</h2>
			<div id="reg-error" class="form-error"></div>
			<div id="reg-success" class="form-error" style="color:var(--accent-primary);background:rgba(0,255,136,0.1)"></div>
			<form onsubmit="return doRegister(event)">
				<div class="form-group">
					<label for="reg-token">Registration Token</label>
					<input type="text" id="reg-token" placeholder="Paste token from admin">
				</div>
				<div class="form-group">
					<label for="reg-username">Username</label>
					<input type="text" id="reg-username" placeholder="Letters, numbers, _ or - (3+ chars)">
				</div>
				<div class="form-group">
					<label for="reg-password">Password</label>
					<input type="password" id="reg-password" placeholder="Choose a password (8+ chars)">
				</div>
				<button type="submit" class="btn-login">Create Account</button>
			</form>
			<div class="login-footer register-link">
				<a onclick="hideRegSection()">Back to sign in</a>
			</div>
		</div>
	</div>
	<script>
		function doLogin(e) {
			e.preventDefault();
			var errEl = document.getElementById('login-error');
			var btn = document.getElementById('login-btn');
			errEl.style.display = 'none';
			btn.disabled = true;
			btn.textContent = 'Signing in...';
			fetch('/api/auth/login', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					username: document.getElementById('username').value,
					password: document.getElementById('password').value
				})
			})
			.then(function(res) { return res.json().then(function(d) { return { ok: res.ok, data: d }; }); })
			.then(function(r) {
				if (r.ok && r.data.success) {
					window.location.href = '/';
				} else {
					errEl.textContent = r.data.error || 'Login failed';
					errEl.style.display = 'block';
					btn.disabled = false;
					btn.textContent = 'Sign In';
				}
			})
			.catch(function() {
				errEl.textContent = 'Connection error. Please try again.';
				errEl.style.display = 'block';
				btn.disabled = false;
				btn.textContent = 'Sign In';
			});
			return false;
		}
		function showRegSection() {
			document.getElementById('register-section').style.display = 'block';
		}
		function hideRegSection() {
			document.getElementById('register-section').style.display = 'none';
		}
		function doRegister(e) {
			e.preventDefault();
			var errEl = document.getElementById('reg-error');
			var succEl = document.getElementById('reg-success');
			errEl.style.display = 'none';
			succEl.style.display = 'none';
			fetch('/api/auth/register', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					token: document.getElementById('reg-token').value.trim(),
					username: document.getElementById('reg-username').value.trim(),
					password: document.getElementById('reg-password').value
				})
			})
			.then(function(res) { return res.json().then(function(d) { return { ok: res.ok, data: d }; }); })
			.then(function(r) {
				if (r.ok) {
					succEl.textContent = r.data.message + '. You can now sign in above.';
					succEl.style.display = 'block';
				} else {
					errEl.textContent = r.data.error || 'Registration failed';
					errEl.style.display = 'block';
				}
			})
			.catch(function() {
				errEl.textContent = 'Connection error.';
				errEl.style.display = 'block';
			});
			return false;
		}
	</script>
</body>
</html>`

func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" || req.Token == "" {
		jsonError(w, "All fields are required", http.StatusBadRequest)
		return
	}
	if len(req.Username) < 3 || len(req.Username) > 32 {
		jsonError(w, "Username must be 3-32 characters", http.StatusBadRequest)
		return
	}
	if !isValidUsername(req.Username) {
		jsonError(w, "Username may only contain letters, numbers, underscores, and hyphens", http.StatusBadRequest)
		return
	}
	if len(req.Password) < 8 {
		jsonError(w, "Password must be at least 8 characters", http.StatusBadRequest)
		return
	}

	tokenStoreMu.Lock()
	ts := loadTokensUnsafe()
	regToken, exists := ts.Tokens[req.Token]
	if !exists || regToken.Used || time.Now().After(regToken.ExpiresAt) {
		tokenStoreMu.Unlock()
		jsonError(w, "Invalid or expired registration token", http.StatusBadRequest)
		return
	}
	regToken.Used = true
	regToken.UsedBy = req.Username
	ts.Tokens[req.Token] = regToken
	saveTokensUnsafe(ts)
	tokenStoreMu.Unlock()

	userStoreMu.Lock()
	defer userStoreMu.Unlock()
	us := loadUsersUnsafe()
	if _, exists := us.Users[req.Username]; exists {
		jsonError(w, "Username already taken", http.StatusConflict)
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		jsonError(w, "Internal error", http.StatusInternalServerError)
		return
	}
	us.Users[req.Username] = User{
		Username:     req.Username,
		PasswordHash: hash,
		Role:         regToken.Role,
		CreatedAt:    time.Now(),
		CreatedBy:    "token:" + regToken.CreatedBy,
	}
	if err := saveUsersUnsafe(us); err != nil {
		jsonError(w, "Failed to create user", http.StatusInternalServerError)
		return
	}

	log.Printf("User registered: %s (role: %s, invited by: %s)", req.Username, regToken.Role, regToken.CreatedBy)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  fmt.Sprintf("Account '%s' created with %s role", req.Username, regToken.Role),
		"username": req.Username,
		"role":     regToken.Role,
	})
}

func handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		store := loadTokens()
		type info struct {
			Token     string    `json:"token"`
			Role      string    `json:"role"`
			CreatedBy string    `json:"created_by"`
			CreatedAt time.Time `json:"created_at"`
			ExpiresAt time.Time `json:"expires_at"`
			Used      bool      `json:"used"`
			UsedBy    string    `json:"used_by,omitempty"`
			Expired   bool      `json:"expired"`
		}
		tokens := make([]info, 0)
		for _, t := range store.Tokens {
			tokens = append(tokens, info{
				Token: t.Token, Role: t.Role, CreatedBy: t.CreatedBy,
				CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
				Used: t.Used, UsedBy: t.UsedBy,
				Expired: time.Now().After(t.ExpiresAt),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokens)

	case http.MethodPost:
		var req struct {
			Role      string `json:"role"`
			ExpiresIn int    `json:"expires_in_hours"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Role != "admin" && req.Role != "user" {
			req.Role = "user"
		}
		if req.ExpiresIn <= 0 {
			req.ExpiresIn = 168
		}
		b := make([]byte, 18)
		rand.Read(b)
		tokenStr := base64.RawURLEncoding.EncodeToString(b)

		tokenStoreMu.Lock()
		defer tokenStoreMu.Unlock()
		store := loadTokensUnsafe()
		store.Tokens[tokenStr] = RegistrationToken{
			Token: tokenStr, Role: req.Role,
			CreatedBy: r.Header.Get("X-Auth-User"),
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(time.Duration(req.ExpiresIn) * time.Hour),
		}
		saveTokensUnsafe(store)

		log.Printf("Registration token created by %s (role: %s)", r.Header.Get("X-Auth-User"), req.Role)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token": tokenStr, "role": req.Role,
			"expires_at": store.Tokens[tokenStr].ExpiresAt,
		})

	case http.MethodDelete:
		var req struct {
			Token string `json:"token"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		tokenStoreMu.Lock()
		defer tokenStoreMu.Unlock()
		store := loadTokensUnsafe()
		if _, exists := store.Tokens[req.Token]; !exists {
			jsonError(w, "Token not found", http.StatusNotFound)
			return
		}
		delete(store.Tokens, req.Token)
		saveTokensUnsafe(store)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"success": "Token deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		store := loadUsers()
		type info struct {
			Username  string    `json:"username"`
			Role      string    `json:"role"`
			CreatedAt time.Time `json:"created_at"`
			CreatedBy string    `json:"created_by"`
		}
		users := make([]info, 0)
		for _, u := range store.Users {
			users = append(users, info{
				Username: u.Username, Role: u.Role,
				CreatedAt: u.CreatedAt, CreatedBy: u.CreatedBy,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(users)

	case http.MethodDelete:
		var req struct {
			Username string `json:"username"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		currentUser := r.Header.Get("X-Auth-User")
		if req.Username == currentUser {
			jsonError(w, "Cannot delete your own account", http.StatusBadRequest)
			return
		}

		userStoreMu.Lock()
		defer userStoreMu.Unlock()
		store := loadUsersUnsafe()
		target, exists := store.Users[req.Username]
		if !exists {
			jsonError(w, "User not found", http.StatusNotFound)
			return
		}
		if target.Role == "admin" {
			adminCount := 0
			for _, u := range store.Users {
				if u.Role == "admin" {
					adminCount++
				}
			}
			if adminCount <= 1 {
				jsonError(w, "Cannot delete the last admin user", http.StatusBadRequest)
				return
			}
		}
		delete(store.Users, req.Username)
		saveUsersUnsafe(store)

		log.Printf("User %s deleted by %s", req.Username, currentUser)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"success": fmt.Sprintf("User '%s' deleted", req.Username)})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
