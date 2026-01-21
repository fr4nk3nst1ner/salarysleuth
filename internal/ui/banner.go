package ui

import (
	"fmt"
	"math/rand"
//	"strconv"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/utils"
)

const bannerText = `
▄▄███▄▄· █████╗ ██╗      █████╗ ██████╗ ██╗   ██╗    ▄▄███▄▄·██╗     ███████╗██╗   ██╗████████╗██╗  ██╗
██╔════╝██╔══██╗██║     ██╔══██╗██╔══██╗╚██╗ ██╔╝    ██╔════╝██║     ██╔════╝██║   ██║╚══██╔══╝██║  ██║
███████╗███████║██║     ███████║██████╔╝ ╚████╔╝     ███████╗██║     █████╗  ██║   ██║   ██║   ███████║
╚════██║██╔══██║██║     ██╔══██║██╔══██╗  ╚██╔╝      ╚════██║██║     ██╔══╝  ██║   ██║   ██║   ██╔══██║
███████║██║  ██║███████╗██║  ██║██║  ██║   ██║       ███████║███████╗███████╗╚██████╔╝   ██║   ██║  ██║
╚═▀▀▀══╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝       ╚═▀▀▀══╝╚══════╝╚══════╝ ╚═════╝    ╚═╝   ╚═╝  ╚═╝
 @fr4nk3nst1ner                                                                                                        
`

// ColorizeText applies random colors to the input text
func ColorizeText(text string) string {
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)

	startColor := pterm.NewRGB(uint8(random.Intn(256)), uint8(random.Intn(256)), uint8(random.Intn(256)))
	firstPoint := pterm.NewRGB(uint8(random.Intn(256)), uint8(random.Intn(256)), uint8(random.Intn(256)))

	strs := strings.Split(text, "")

	var coloredText string
	for i := 0; i < len(text); i++ {
		if i < len(strs) {
			coloredText += startColor.Fade(0, float32(len(text)), float32(i%(len(text)/2)), firstPoint).Sprint(strs[i])
		}
	}

	return coloredText
}

// PrintBanner displays the application banner
func PrintBanner(silence bool) {
	if !silence {
		coloredBanner := ColorizeText(bannerText)
		fmt.Println(coloredBanner)
	}
}

// FormatURL formats a URL, optionally as a clickable terminal hyperlink using OSC 8 escape sequence
func FormatURL(url string, useHyperlink bool) string {
	if !useHyperlink {
		return url
	}
	// OSC 8 terminal hyperlink format: \033]8;;URL\033\\TEXT\033]8;;\033\\
	// Using \a (BEL) as the terminator for wider compatibility
	return fmt.Sprintf("\033]8;;%s\a%s\033]8;;\a", url, "View Job")
}

// ColorizeSalary applies color formatting to salary strings
func ColorizeSalary(salary string) string {
	if salary == "" || salary == "Not Available" {
		return pterm.Red("Not Available")
	}

	// Convert salary string to numeric for comparison
	numericValue := utils.ExtractNumericValue(salary)
	
	// Format the salary with commas
	formattedSalary := utils.FormatSalary(salary)

	switch {
	case numericValue >= 400000:
		return pterm.Green(formattedSalary) // Dark green for >$400K
	case numericValue >= 300000:
		return pterm.LightGreen(formattedSalary) // Light green for $300K-$400K
	case numericValue >= 100000:
		return pterm.Yellow(formattedSalary) // Yellow for $100K-$300K
	default:
		return pterm.Red(formattedSalary) // Red for <$100K
	}
} 