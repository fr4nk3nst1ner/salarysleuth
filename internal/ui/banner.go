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