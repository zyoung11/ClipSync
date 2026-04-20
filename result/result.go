package result

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type RadioConfig struct {
	Question string
	Options  []string
}

func RadioList(config RadioConfig) string {
	m := model{
		config: config,
		cursor: 0,
	}

	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		fmt.Println("Oh no:", err)
		os.Exit(1)
	}

	if m, ok := finalModel.(model); ok && m.choice != "" {
		return m.choice
	}

	return ""
}

type model struct {
	config RadioConfig
	cursor int
	choice string
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit

		case "enter":
			// 确保光标在有效范围内
			if m.cursor >= 0 && m.cursor < len(m.config.Options) {
				m.choice = m.config.Options[m.cursor]
			}
			return m, tea.Quit

		case "down", "j":
			m.cursor++
			if m.cursor >= len(m.config.Options) {
				m.cursor = 0
			}

		case "up", "k":
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(m.config.Options) - 1
			}
		}
	}

	return m, nil
}

func (m model) View() string {
	s := strings.Builder{}

	s.WriteString(m.config.Question + "\n\n")

	for i, choice := range m.config.Options {
		if m.cursor == i {
			s.WriteString("(•) ")
		} else {
			s.WriteString("( ) ")
		}
		s.WriteString(choice)
		s.WriteString("\n")
	}
	s.WriteString("\n(press q to quit)\n")

	return s.String()
}

// 教程
// package main

// import (
// 	"fmt"
// 	"main/result"
// )

// func main() {
// 	config := result.RadioConfig{
// 		Question: "What kind of Bubble Tea would you like to order?",
// 		Options:  []string{"Taro", "Coffee", "Lychee", "Milk Tea", "Matcha"},
// 	}

// 	result := result.RadioList(config)

// 	if result != "" {
// 		fmt.Printf("\n---\nYou chose %s!\n", result)
// 	} else {
// 		fmt.Println("\nOperation cancelled.")
// 	}
// }
