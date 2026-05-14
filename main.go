package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	borderStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("62"))
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	typeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	ctfStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	treeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
)

// Optimized out-of-the-box Vim config (Temporary)
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
	viewMode      string // "list" or "tree"
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
				m.viewMode = "tree"
			} else {
				m.viewMode = "list"
			}
		case "up", "ctrl+k":
			if m.cursor > 0 {
				m.cursor--
				m.updatePreview()
			}
		case "down", "ctrl+j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				m.updatePreview()
			}
		case "enter":
			if len(m.filtered) > 0 {
				return m, tea.ExecProcess(exec.Command("less", "-R", m.filtered[m.cursor].fullPath), func(err error) tea.Msg { return nil })
			}
		case "ctrl+v": // VIM with Zero-Plugin Optimized Config
			if len(m.filtered) > 0 {
				tmpVimrc, _ := os.CreateTemp("", "virc")
				tmpVimrc.WriteString(vimConfig)
				tmpVimrc.Close()
				return m, tea.ExecProcess(exec.Command("vim", "-u", tmpVimrc.Name(), m.filtered[m.cursor].fullPath), func(err error) tea.Msg {
					os.Remove(tmpVimrc.Name())
					return nil
				})
			}
		case "ctrl+s":
			if len(m.filtered) > 0 {
				_ = exec.Command("tmux", "split-window", "-h", fmt.Sprintf("less -R '%s'", m.filtered[m.cursor].fullPath)).Start()
			}
		case "ctrl+d":
			if len(m.filtered) > 0 {
				dest := m.filtered[m.cursor].fileName
				_ = copyFile(m.filtered[m.cursor].fullPath, dest)
				m.statusMessage = "💾 Exported to ./" + dest
			}
		case "ctrl+o":
			if m.repoURL != "" {
				cleanURL := strings.TrimSuffix(m.repoURL, ".git")
				openBrowser(fmt.Sprintf("%s/blob/%s/%s", cleanURL, m.branch, m.filtered[m.cursor].relPath))
			}
		default:
			oldVal := m.input.Value()
			m.input, _ = m.input.Update(msg)
			if m.input.Value() != oldVal {
				m.filterFiles()
				m.updatePreview()
			}
		}
	}
	m.viewport, _ = m.viewport.Update(msg)
	return m, nil
}

func (m model) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	preview := borderStyle.Width(m.width - 2).Height(int(float64(m.height)*0.55) - 2).Render(m.viewport.View())

	var listBody string
	maxItems := m.height - int(float64(m.height)*0.55) - 5

	if m.viewMode == "tree" {
		listBody = m.renderTree(maxItems)
	} else {
		listBody = m.renderList(maxItems)
	}

	help := headerStyle.Render(" [TAB] Toggle Tree/List | [Enter] Less | [Ctrl+V] Vim (Opt) | [Ctrl+D] DL | [Ctrl+O] Link")
	prompt := fmt.Sprintf(" ⚡ Search: %s  %s", m.input.View(), m.statusMessage)
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

func (m model) renderTree(limit int) string {
	var lines []string
	currType, currCTF := "", ""
	for i, item := range m.filtered {
		if len(lines) >= limit {
			break
		}
		if item.wType != currType {
			currType = item.wType
			lines = append(lines, treeStyle.Render("📂 "+currType))
		}
		if item.ctfName != currCTF {
			currCTF = item.ctfName
			lines = append(lines, "  ┗━ "+ctfStyle.Render("📦 "+currCTF))
		}
		prefix := "     "
		if i == m.cursor {
			prefix = "   ▶ \033[36m"
		}
		lines = append(lines, prefix+item.fileName+"\033[0m")
	}
	return strings.Join(lines, "\n")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
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

func padString(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: ctf-tui <local_directory_path OR github_repository_url>")
		os.Exit(1)
	}

	target := os.Args[1]
	var repoURL string
	var currentBranch string

	// Handle automated GitHub repository link parsing
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "git@") {
		repoURL = target
		fmt.Printf("[*] Cloning online repository down to local storage: %s\n", target)
		
		tempDir, err := os.MkdirTemp("", "ctf-repo-")
		if err != nil {
			fmt.Printf("[-] Failed to create system temporary workspace: %v\n", err)
			os.Exit(1)
		}
		
		// Run a fast, shallow clone to minimize download sizes
		cloneCmd := exec.Command("git", "clone", "--depth", "1", repoURL, tempDir)
		cloneCmd.Stdout = os.Stdout
		cloneCmd.Stderr = os.Stderr
		if err := cloneCmd.Run(); err != nil {
			fmt.Printf("[-] Git operations failed: %v\n", err)
			os.Exit(1)
		}
		
		// Read the branch name to construct correct URLs for browser links
		branchCmd := exec.Command("git", "-C", tempDir, "rev-parse", "--abbrev-ref", "HEAD")
		if out, err := branchCmd.Output(); err == nil {
			currentBranch = strings.TrimSpace(string(out))
		} else {
			currentBranch = "main"
		}
		
		target = tempDir
		// Delete the temporary clone automatically when the TUI exits
		defer os.RemoveAll(tempDir)
	}

	var items []writeupItem
	_ = filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") && !strings.Contains(path, "/.git/") {
			rel, _ := filepath.Rel(target, path)
			parts := strings.Split(rel, string(filepath.Separator))
			
			wType := "General"
			ctfName := "Misc"
			fileName := info.Name()

			// Smart parsing of path hierarchy (e.g., Exploitation/AC/challenge.md)
			if len(parts) >= 3 {
				wType = parts[0]
				ctfName = parts[1]
			} else if len(parts) == 2 {
				wType = parts[0]
				ctfName = "Root"
			}

			items = append(items, writeupItem{
				fullPath: path,
				relPath:  rel,
				wType:    wType,
				ctfName:  ctfName,
				fileName: fileName,
			})
		}
		return nil
	})

	if len(items) == 0 {
		fmt.Println("[-] No Markdown files found inside the target destination.")
		os.Exit(1)
	}

	ti := textinput.New()
	ti.Placeholder = "Search (e.g. 'Exploitation AC')..."
	ti.Focus()

	m := model{
		items:    items,
		filtered: items,
		input:    ti,
		viewport: viewport.New(0, 0),
		repoURL:  repoURL,
		branch:   currentBranch,
		viewMode: "list",
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Runtime failure: %v\n", err)
		os.Exit(1)
	}
}