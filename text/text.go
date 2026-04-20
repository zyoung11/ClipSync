package text

import (
	"log"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TextInput(header string) string {
	p := tea.NewProgram(initialModel(header))
	m, err := p.Run()
	if err != nil {
		log.Fatal(err)
	}

	if finalModel, ok := m.(model); ok {
		return finalModel.textInput.Value()
	}
	return ""
}

type (
	errMsg error
)

type model struct {
	textInput textinput.Model
	header    string
	err       error
	quitting  bool
}

func initialModel(header string) model {
	ti := textinput.New()
	ti.Placeholder = "Pikachu"
	ti.SetVirtualCursor(false)
	ti.Focus()
	ti.CharLimit = 156
	ti.SetWidth(20)

	return model{
		textInput: ti,
		header:    header,
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		}
	}

	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) View() tea.View {
	var c *tea.Cursor
	if !m.textInput.VirtualCursor() {
		c = m.textInput.Cursor()
		c.Y += lipgloss.Height(m.headerView())
	}

	str := lipgloss.JoinVertical(lipgloss.Top, m.headerView(), m.textInput.View(), m.footerView())
	if m.quitting {
		str += "\n"
	}

	v := tea.NewView(str)
	v.Cursor = c
	return v
}

func (m model) headerView() string { return "\n" + m.header + "\n" }
func (m model) footerView() string { return "\n(esc to quit)\n" }

// 教程
// package main

// import (
// 	"fmt"
// 	"main/text"
// )

// func main() {
// 	text := text.TextInput("请输入：")
// 	fmt.Println(text)
// }
