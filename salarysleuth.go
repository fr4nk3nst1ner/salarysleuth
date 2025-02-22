package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	// Get the directory of the current source file
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Println("Error getting current file path")
		os.Exit(1)
	}
	
	// Get the project root directory (parent of the current file)
	projectRoot := filepath.Dir(filename)

	// Build the path to the main.go file
	mainPath := filepath.Join(projectRoot, "cmd", "salarysleuth", "main.go")

	// Create the command to run the main program
	cmd := exec.Command("go", "run", mainPath)

	// Pass through all command line arguments
	cmd.Args = append(cmd.Args, os.Args[1:]...)

	// Set up standard input/output
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the command
	if err := cmd.Run(); err != nil {
		fmt.Printf("Error running main program: %v\n", err)
		os.Exit(1)
	}
}
