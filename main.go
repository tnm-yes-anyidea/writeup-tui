package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// Styles for a modern 2026 terminal look
var (
	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	typeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	ctfStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	treeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
)

const vimConfig = `
set nocompatible
syntax on
set number
set relativenumber
set expandtab
set tabstop=4
set shiftwidth=4
set smartindent
set cursorline
set termguicolors
set mouse=a
set background=dark
`

type writeupItem struct {
	fullPath string
	relPath  string
	wType    string
	ctfName  string
	fileName string
}

type model struct {
	items         []writeupItem
	filtered      []writeupItem
	cursor        int
	input         textinput.Model
	viewport      viewport.Model
	width, height int
	repoURL       string
	branch        string
	statusMessage string
	targetPath    string
	viewMode      string // "list", "groups", or "fulltree"
}

// GetDirectoryTree generates a recursive visual tree of the workspace
func GetDirectoryTree(root string, indent string, isLast bool) string {
	info, err := os.Stat(root)
	if err != nil {
		return ""
	}

	name := filepath.Base(root)
	var result string

	if indent != "" {
		marker := "├── "
		if isLast {
			marker = "└── "
		}
		result += indent + marker + name + "\n"
	} else {
		result += "📂 " + name + "\n"
	}

	if !info.IsDir() {
		return result
	}

	files, _ := os.ReadDir(root)
	var filteredFiles []os.DirEntry
	for _, f := range files {
		// Ignore hidden files and .git
		if !strings.HasPrefix(f.Name(), ".") && f.Name() != ".git" {
			filteredFiles = append(filteredFiles, f)
		}
	}

	newIndent := indent
	if indent != "" {
		if isLast {
			newIndent += "    "
		} else {
			newIndent += "│   "
		}
	}

	for i, entry := range filteredFiles {
		result += GetDirectoryTree(filepath.Join(root, entry.Name()), newIndent, i == len(filteredFiles)-1)
	}
	return result
}

func (m *model) filterFiles() {
	query := strings.ToLower(m.input.Value())
	if query == "" {
		m.filtered = m.items
		return
	}
	terms := strings.Fields(query)
	var matched []writeupItem
	for _, item := range m.items {
		searchSpace := strings.ToLower(fmt.Sprintf("%s %s %s", item.wType, item.ctfName, item.fileName))
		matchAll := true
		for _, term := range terms {
			if !strings.Contains(searchSpace, term) {
				matchAll = false
				break
			}
		}
		if matchAll {
			matched = append(matched, item)
		}
	}
	m.filtered = matched
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
}

func (m *model) updatePreview() {
	if m.viewMode == "fulltree" {
		m.viewport.SetContent(GetDirectoryTree(m.targetPath, "", true))
		m.viewport.GotoTop()
		return
	}

	if len(m.filtered) == 0 {
		m.viewport.SetContent("No matching writeups found.")
		return
	}
	content, _ := os.ReadFile(m.filtered[m.cursor].fullPath)
	renderer, _ := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(m.viewport.Width-4))
	rendered, _ := renderer.Render(string(content))
	m.viewport.SetContent(rendered)
	m.viewport.GotoTop()
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width, m.viewport.Height = msg.Width-4, int(float64(msg.Height)*0.55)-2
		m.updatePreview()

	case tea.KeyMsg:
		m.statusMessage = ""
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "tab":
			if m.viewMode == "list" {
				m.viewMode = "groups"
			} else {
				m.viewMode = "list"
			}
		case "ctrl+t":
			if m.viewMode != "fulltree" {
				m.viewMode = "fulltree"
			} else {
				m.viewMode = "list"
			}
			m.updatePreview()
		case "up", "ctrl+k":
			if m.viewMode == "fulltree" {
				m.viewport.LineUp(1)
			} else if m.cursor > 0 {
				m.cursor--
				m.updatePreview()
			}
		case "down", "ctrl+j":
			if m.viewMode == "fulltree" {
				m.viewport.LineDown(1)
			} else if m.cursor < len(m.filtered)-1 {
				m.cursor++
				m.updatePreview()
			}
		case "enter":
			if m.viewMode != "fulltree" && len(m.filtered) > 0 {
				return m, tea.ExecProcess(exec.Command("less", "-R", m.filtered[m.cursor].fullPath), func(err error) tea.Msg { return nil })
			}
		case "ctrl+v":
			if m.viewMode != "fulltree" && len(m.filtered) > 0 {
				tmpVimrc, _ := os.CreateTemp("", "virc")
				tmpVimrc.WriteString(vimConfig)
				tmpVimrc.Close()
				return m, tea.ExecProcess(exec.Command("vim", "-u", tmpVimrc.Name(), m.filtered[m.cursor].fullPath), func(err error) tea.Msg {
					os.Remove(tmpVimrc.Name())
					return nil
				})
			}
		case "ctrl+o":
			if m.viewMode != "fulltree" && m.repoURL != "" {
				cleanURL := strings.TrimSuffix(m.repoURL, ".git")
				openBrowser(fmt.Sprintf("%s/blob/%s/%s", cleanURL, m.branch, m.filtered[m.cursor].relPath))
			}
		default:
			if m.viewMode != "fulltree" {
				oldVal := m.input.Value()
				m.input, _ = m.input.Update(msg)
				if m.input.Value() != oldVal {
					m.filterFiles()
					m.updatePreview()
				}
			}
		}
	}
	m.viewport, _ = m.viewport.Update(msg)
	return m, nil
}

func (m model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	// Fix for Title issue: Define style then render
	var viewTitle string
	if m.viewMode == "fulltree" {
		viewTitle = " Repository Tree (Scroll with Arrows) "
	} else {
		viewTitle = " Writeup Preview "
	}

	// Apply style with dynamic width/height
	styledBox := borderStyle.Copy().
		Width(m.width - 2).
		Height(int(float64(m.height)*0.55) - 2)

	// Combine components
	preview := styledBox.Render(fmt.Sprintf("%s\n%s", typeStyle.Render(viewTitle), m.viewport.View()))

	var listBody string
	maxLines := m.height - int(float64(m.height)*0.55) - 5

	switch m.viewMode {
	case "groups":
		listBody = m.renderGrouped(maxLines)
	case "list":
		listBody = m.renderList(maxLines)
	default:
		listBody = "\n  " + ctfStyle.Render("[ Tree Mode Active - Ctrl+T to exit ]")
	}

	help := headerStyle.Render(" [TAB] View | [Ctrl+T] Tree | [Enter] Less | [Ctrl+V] Vim | [Ctrl+O] Browser")
	prompt := fmt.Sprintf(" ⚡ Search: %s %s", m.input.View(), m.statusMessage)

	return lipgloss.JoinVertical(lipgloss.Left, preview, help, listBody, prompt)
}

func (m model) renderList(limit int) string {
	var lines []string
	for i, item := range m.filtered {
		if len(lines) >= limit {
			break
		}
		line := fmt.Sprintf("[%s] [%s] %s", typeStyle.Render(item.wType), ctfStyle.Render(item.ctfName), item.fileName)
		if i == m.cursor {
			lines = append(lines, "▶ \033[36m"+line+"\033[0m")
		} else {
			lines = append(lines, "  "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderGrouped(limit int) string {
	var lines []string
	currT, currC := "", ""
	for i, item := range m.filtered {
		if len(lines) >= limit {
			break
		}
		if item.wType != currT {
			currT = item.wType
			lines = append(lines, treeStyle.Render("📂 "+currT))
		}
		if item.ctfName != currC {
			currC = item.ctfName
			lines = append(lines, "  ┗━ "+ctfStyle.Render("📦 "+currC))
		}
		prefix := "     "
		if i == m.cursor {
			prefix = "   ▶ \033[36m"
		}
		lines = append(lines, prefix+item.fileName+"\033[0m")
	}
	return strings.Join(lines, "\n")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func normalizeType(wType string) string {
	typeMap := map[string]string{
		"webexploitation":  "WebExploitation",
		"pwn":              "Exploitation(PWN)",
		"cloudsecurity":    "CloudSecurity",
		"digitalforensics": "DigitalForensics",
		"generalskills":    "GeneralSkills",
		"cryptography":     "Cryptography",
	}
	norm := regexp.MustCompile(`[\s_\-\(\)]+`).ReplaceAllString(strings.ToLower(wType), "")
	if val, ok := typeMap[norm]; ok {
		return val
	}
	return strings.Title(wType)
}

func isIndexFile(fileName, categoryDir string) bool {
	name := strings.ToLower(strings.TrimSuffix(fileName, ".md"))
	normFile := regexp.MustCompile(`[\s_\-\(\)]+`).ReplaceAllString(name, "")
	normCat := regexp.MustCompile(`[\s_\-\(\)]+`).ReplaceAllString(strings.ToLower(categoryDir), "")

	if normFile == "index" || normFile == "readme" {
		return true
	}
	if normCat != "" && len(normFile) >= 3 && (strings.Contains(normCat, normFile) || strings.Contains(normFile, normCat)) {
		return true
	}
	return false
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: ctf-tui <path or url>")
		os.Exit(1)
	}

	target := os.Args[1]
	var repo, branch string

	if strings.Contains(target, "http") || strings.HasPrefix(target, "git@") {
		repo = target
		temp, _ := os.MkdirTemp("", "ctf-tui-")
		_ = exec.Command("git", "clone", "--depth", "1", repo, temp).Run()
		target = temp
		out, _ := exec.Command("git", "-C", temp, "rev-parse", "--abbrev-ref", "HEAD").Output()
		branch = strings.TrimSpace(string(out))
		defer os.RemoveAll(temp)
	}

	var items []writeupItem
	filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") && !strings.Contains(path, "/.git/") {
			rel, _ := filepath.Rel(target, path)
			parts := strings.Split(rel, string(filepath.Separator))
			wType, ctfName, catDir := "General", "Misc", ""

			if len(parts) >= 1 {
				catDir = parts[0]
				wType = normalizeType(parts[0])
			}

			if isIndexFile(info.Name(), catDir) {
				ctfName = "📚 Index Files"
			} else if len(parts) >= 3 {
				ctfName = parts[1]
			} else if len(parts) == 2 {
				ctfName = "Root"
			}

			items = append(items, writeupItem{path, rel, wType, ctfName, info.Name()})
		}
		return nil
	})

	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.Focus()

	m := model{
		items:      items,
		filtered:   items,
		input:      ti,
		viewport:   viewport.New(0, 0),
		repoURL:    repo,
		branch:     branch,
		viewMode:   "list",
		targetPath: target,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
	}
}
