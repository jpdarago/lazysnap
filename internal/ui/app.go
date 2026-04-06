package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jpdarago/lazysnap/internal/cache"
	"github.com/jpdarago/lazysnap/internal/tarsnap"
)

type panel int

const (
	archivePanel panel = iota
	detailPanel
)

// Model is the root Bubble Tea model.
type Model struct {
	tarsnap *tarsnap.Client
	cache   *cache.DB

	archives     []tarsnap.Archive
	cursor       int
	activePanel  panel
	stats        *tarsnap.ArchiveStats
	files        []tarsnap.FileEntry
	loading      bool
	errMsg       string
	statusMsg    string
	width        int
	height       int
	filterInput  textinput.Model
	filtering    bool
	filterText   string
	confirmMsg   string
	confirming   bool
	confirmAction func() tea.Msg

	passphraseInput textinput.Model
	needPassphrase  bool

	searchInput   textinput.Model
	searching     bool
	searchResults []searchResult
	showSearch    bool

	promptInput  textinput.Model
	promptMode   promptMode
	promptLabel  string
	createName   string // stored between create prompts

	debug     *debugLog
	showDebug bool

	restoring       bool   // true while a restore is in progress
	restoreDone     bool   // true briefly after restore completes
	restoreProgress string // current progress line during restore
}

// NewModel creates a new root model.
func NewModel(tc *tarsnap.Client, db *cache.DB) Model {
	ti := textinput.New()
	ti.Placeholder = "filter archives..."
	ti.CharLimit = 100

	pi := textinput.New()
	pi.Placeholder = "enter passphrase..."
	pi.EchoMode = textinput.EchoPassword
	pi.EchoCharacter = '•'
	pi.CharLimit = 256

	pi.Focus()

	si := textinput.New()
	si.Placeholder = "search files across archives..."
	si.CharLimit = 200

	pri := textinput.New()
	pri.CharLimit = 500

	dl := newDebugLog()
	dl.log("lazysnap starting")
	dl.log("keyfile=%q", tc.Keyfile)
	tc.Log = dl.log

	return Model{
		tarsnap:         tc,
		cache:           db,
		filterInput:     ti,
		passphraseInput: pi,
		searchInput:     si,
		promptInput:     pri,
		needPassphrase:  true,
		debug:           dl,
	}
}

// Init loads archives on startup.
func (m Model) Init() tea.Cmd {
	if m.needPassphrase {
		return textinput.Blink
	}
	return m.loadArchives()
}

// NotifyFromDebugLog wires the debug log to send messages to the tea.Program
// so the UI re-renders when new debug lines arrive during long-running commands.
// This must be called before p.Run(). It works because debug is a shared pointer.
func (m *Model) NotifyFromDebugLog(p *tea.Program) {
	m.debug.prog = p
	m.debug.notify = func() {
		// Must be non-blocking: this is called from inside tea.Cmd goroutines.
		// p.Send blocks on an unbuffered channel, which would deadlock if
		// bubbletea's event loop is waiting for the Cmd to return.
		go p.Send(debugTickMsg{})
	}
}

// --- Messages ---

type archivesLoadedMsg struct {
	archives []tarsnap.Archive
}

type statsLoadedMsg struct {
	stats *tarsnap.ArchiveStats
}

type filesLoadedMsg struct {
	files []tarsnap.FileEntry
}

type errMsg struct {
	err error
}

type archiveDeletedMsg struct {
	name string
}

// debugTickMsg is sent to trigger a re-render when new debug log lines arrive.
type debugTickMsg struct{}

type searchResult struct {
	Archive string
	Files   []string
}

type searchCompleteMsg struct {
	query   string
	results []searchResult
}

type promptMode int

const (
	promptNone promptMode = iota
	promptRestore
	promptCreateName
	promptCreatePath
	promptBasePath
)

type archiveRestoredMsg struct {
	archive string
	target  string
}

type archiveCreatedMsg struct {
	name string
}

type restoreProgressMsg struct {
	line string
}

type restoreDoneDismissMsg struct{}

// --- Update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.debug.log("window resize: %dx%d", msg.Width, msg.Height)
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case archivesLoadedMsg:
		m.debug.log("archives loaded: %d archives", len(msg.archives))
		sort.Slice(msg.archives, func(i, j int) bool {
			return msg.archives[i].Name < msg.archives[j].Name
		})
		m.archives = msg.archives
		m.loading = false
		m.errMsg = ""
		m.statusMsg = ""
		if len(m.archives) > 0 {
			m.cursor = 0
			return m, m.loadDetail()
		}
		return m, nil

	case statsLoadedMsg:
		m.debug.log("stats loaded for %q: total=%d compressed=%d",
			msg.stats.ArchiveName, msg.stats.TotalSize, msg.stats.CompressedSize)
		m.stats = msg.stats
		m.errMsg = ""
		return m, nil

	case filesLoadedMsg:
		m.debug.log("files loaded: %d files", len(msg.files))
		m.files = msg.files
		m.errMsg = ""
		return m, nil

	case archiveDeletedMsg:
		m.debug.log("archive deleted: %q", msg.name)
		m.confirming = false
		m.confirmMsg = ""
		m.errMsg = ""
		return m, m.loadArchives()

	case errMsg:
		m.debug.log("ERROR: %v", msg.err)
		m.loading = false
		m.restoring = false
		m.restoreProgress = ""
		m.errMsg = msg.err.Error()
		return m, nil

	case debugTickMsg:
		return m, nil

	case restoreProgressMsg:
		m.restoreProgress = msg.line
		return m, nil

	case archiveRestoredMsg:
		m.debug.log("archive %q restored to %q", msg.archive, msg.target)
		m.loading = false
		m.restoring = false
		m.restoreDone = true
		m.restoreProgress = fmt.Sprintf("Restored %q to %s", msg.archive, msg.target)
		m.errMsg = ""
		m.statusMsg = m.restoreProgress
		m.cache.SetConfig("last_restore_dir", msg.target)
		m.cache.SetRestored(msg.archive, msg.target)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return restoreDoneDismissMsg{}
		})

	case restoreDoneDismissMsg:
		m.restoreDone = false
		m.restoreProgress = ""
		return m, nil

	case archiveCreatedMsg:
		m.debug.log("archive %q created", msg.name)
		m.loading = false
		m.errMsg = ""
		m.statusMsg = fmt.Sprintf("Created archive %q", msg.name)
		return m, m.refreshArchives()

	case searchCompleteMsg:
		m.debug.log("search complete: %d archives match %q", len(msg.results), msg.query)
		m.searchResults = msg.results
		m.showSearch = true
		m.loading = false
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "f2" || msg.String() == "ctrl+d" {
			m.showDebug = !m.showDebug
			m.debug.log("debug panel toggled: %v", m.showDebug)
			return m, nil
		}
		if (m.restoring || m.restoreDone) && !m.confirming {
			// Block all input while restore is in progress (except debug toggle above and quit).
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.needPassphrase {
			return m.updatePassphrase(msg)
		}
		if m.confirming {
			return m.updateConfirm(msg)
		}
		if m.promptMode != promptNone {
			return m.updatePrompt(msg)
		}
		if m.searching {
			return m.updateSearch(msg)
		}
		if m.showSearch {
			return m.updateSearchResults(msg)
		}
		if m.filtering {
			return m.updateFilter(msg)
		}
		return m.updateNormal(msg)
	}

	return m, nil
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredArchives()

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		if m.cursor < len(filtered)-1 {
			m.cursor++
			return m, m.loadDetail()
		}

	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			return m, m.loadDetail()
		}

	case "tab":
		if m.activePanel == archivePanel {
			m.activePanel = detailPanel
		} else {
			m.activePanel = archivePanel
		}

	case "R":
		m.loading = true
		return m, m.refreshArchives()

	case "/":
		m.filtering = true
		m.filterInput.Focus()
		return m, textinput.Blink

	case "s":
		m.searching = true
		m.searchInput.SetValue("")
		m.searchInput.Focus()
		return m, textinput.Blink

	case "r":
		if len(filtered) > 0 && m.cursor < len(filtered) {
			m.promptMode = promptRestore
			m.promptLabel = fmt.Sprintf("Restore %q to directory:", filtered[m.cursor].Name)
			m.promptInput.Placeholder = "/path/to/restore"
			defaultDir := m.cache.GetConfig("last_restore_dir")
			if defaultDir == "" {
				defaultDir = m.cache.GetConfig("base_path")
			}
			m.promptInput.SetValue(defaultDir)
			m.promptInput.CursorEnd()
			m.promptInput.Focus()
			return m, textinput.Blink
		}

	case "c":
		m.promptMode = promptCreateName
		m.promptLabel = "New archive name:"
		m.promptInput.Placeholder = "my-backup-2026-04-05"
		m.promptInput.SetValue("")
		m.promptInput.Focus()
		return m, textinput.Blink

	case "B":
		m.promptMode = promptBasePath
		m.promptLabel = "Set base directory for restore/create:"
		m.promptInput.Placeholder = "/path/to/base"
		m.promptInput.SetValue(m.cache.GetConfig("base_path"))
		m.promptInput.CursorEnd()
		m.promptInput.Focus()
		return m, textinput.Blink

	case "d":
		if len(filtered) > 0 && m.cursor < len(filtered) {
			name := filtered[m.cursor].Name
			m.confirming = true
			m.confirmMsg = fmt.Sprintf("Delete archive %q? (y/n)", name)
			m.confirmAction = func() tea.Msg {
				if err := m.tarsnap.DeleteArchive(name); err != nil {
					return errMsg{err}
				}
				m.cache.DeleteArchive(name)
				return archiveDeletedMsg{name}
			}
		}

	case "g":
		m.cursor = 0
		return m, m.loadDetail()

	case "G":
		if len(filtered) > 0 {
			m.cursor = len(filtered) - 1
			return m, m.loadDetail()
		}
	}

	return m, nil
}

func (m Model) updatePassphrase(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "enter":
		m.debug.log("passphrase entered, loading archives...")
		m.tarsnap.Passphrase = m.passphraseInput.Value()
		m.needPassphrase = false
		m.passphraseInput.Blur()
		m.loading = true
		return m, m.loadArchives()
	}

	var cmd tea.Cmd
	m.passphraseInput, cmd = m.passphraseInput.Update(msg)
	return m, cmd
}

func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc":
		m.filtering = false
		m.filterText = m.filterInput.Value()
		m.filterInput.Blur()
		m.cursor = 0
		if len(m.filteredArchives()) > 0 {
			return m, m.loadDetail()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.filterText = m.filterInput.Value()
	return m, cmd
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		action := m.confirmAction
		m.confirming = false
		m.confirmMsg = ""
		m.confirmAction = nil
		m.loading = true
		if m.restoring {
			m.restoreProgress = "Starting restore..."
		}
		return m, func() tea.Msg { return action() }
	case "n", "N", "esc":
		m.confirming = false
		m.confirmMsg = ""
		m.confirmAction = nil
		m.restoring = false
		m.restoreProgress = ""
	}
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.searching = false
		m.searchInput.Blur()
		return m, nil
	case "enter":
		query := m.searchInput.Value()
		if query == "" {
			m.searching = false
			m.searchInput.Blur()
			return m, nil
		}
		m.searching = false
		m.searchInput.Blur()
		m.loading = true
		m.debug.log("searching files for %q", query)
		return m, m.runSearch(query)
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m Model) updateSearchResults(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.showSearch = false
		m.searchResults = nil
		return m, nil
	}
	return m, nil
}

func (m Model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.promptMode = promptNone
		m.promptInput.Blur()
		return m, nil
	case "enter":
		value := m.promptInput.Value()
		if value == "" {
			return m, nil
		}
		m.promptInput.Blur()

		switch m.promptMode {
		case promptRestore:
			m.promptMode = promptNone
			filtered := m.filteredArchives()
			if m.cursor < len(filtered) {
				archiveName := filtered[m.cursor].Name
				if _, err := os.Stat(value); os.IsNotExist(err) {
					m.confirming = true
					m.restoring = true
					m.restoreProgress = "Waiting for confirmation..."
					m.confirmMsg = fmt.Sprintf("Directory %q does not exist. Create it? (y/n)", value)
					m.confirmAction = func() tea.Msg {
						if err := os.MkdirAll(value, 0o755); err != nil {
							return errMsg{fmt.Errorf("create directory: %w", err)}
						}
						onProgress := func(p tarsnap.RestoreProgress) {
							m.debug.send(restoreProgressMsg{line: p.Line})
						}
						if err := m.tarsnap.RestoreWithProgress(archiveName, value, onProgress); err != nil {
							m.debug.log("restore error: %v", err)
							return errMsg{err}
						}
						return archiveRestoredMsg{archive: archiveName, target: value}
					}
					return m, nil
				}
				m.loading = true
				m.restoring = true
				m.restoreProgress = "Starting restore..."
				m.debug.log("restoring %q to %q", archiveName, value)
				return m, m.runRestore(archiveName, value)
			}

		case promptCreateName:
			m.createName = value
			m.promptMode = promptCreatePath
			m.promptLabel = fmt.Sprintf("Path to back up as %q:", m.createName)
			m.promptInput.Placeholder = "/path/to/folder"
			basePath := m.cache.GetConfig("base_path")
			m.promptInput.SetValue(basePath)
			m.promptInput.CursorEnd()
			m.promptInput.Focus()
			return m, textinput.Blink

		case promptCreatePath:
			name := m.createName
			m.promptMode = promptNone
			m.loading = true
			m.debug.log("creating archive %q from %q", name, value)
			return m, m.runCreate(name, value)

		case promptBasePath:
			m.promptMode = promptNone
			m.cache.SetConfig("base_path", value)
			m.debug.log("base path set to %q", value)
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
}

func (m Model) runRestore(archiveName, targetDir string) tea.Cmd {
	return func() tea.Msg {
		onProgress := func(p tarsnap.RestoreProgress) {
			m.debug.send(restoreProgressMsg{line: p.Line})
		}
		if err := m.tarsnap.RestoreWithProgress(archiveName, targetDir, onProgress); err != nil {
			m.debug.log("restore error: %v", err)
			return errMsg{err}
		}
		return archiveRestoredMsg{archive: archiveName, target: targetDir}
	}
}

func (m Model) runCreate(name, path string) tea.Cmd {
	return func() tea.Msg {
		if err := m.tarsnap.CreateArchive(name, []string{path}); err != nil {
			m.debug.log("create error: %v", err)
			return errMsg{err}
		}
		return archiveCreatedMsg{name: name}
	}
}

func (m Model) runSearch(query string) tea.Cmd {
	return func() tea.Msg {
		results, err := m.cache.SearchFiles(query)
		if err != nil {
			m.debug.log("search error: %v", err)
			return errMsg{err}
		}
		// Convert map to sorted slice
		var sr []searchResult
		for archive, files := range results {
			sr = append(sr, searchResult{Archive: archive, Files: files})
		}
		sort.Slice(sr, func(i, j int) bool {
			return sr[i].Archive < sr[j].Archive
		})
		return searchCompleteMsg{query: query, results: sr}
	}
}

// --- View ---

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	if m.showSearch {
		return m.renderSearchResults()
	}

	if m.needPassphrase {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			lipgloss.JoinVertical(lipgloss.Center,
				titleStyle.Render("Lazysnap: Tarsnap...for the truly lazy!"),
				"",
				"Enter tarsnap key passphrase:",
				m.passphraseInput.View(),
				"",
				dimStyle.Render("press enter to continue • ctrl+c to quit"),
			),
		)
	}

	// Layout: left panel (archives) + right panel (detail)
	leftWidth := m.width/3 - 2
	if leftWidth < 20 {
		leftWidth = 20
	}
	rightWidth := m.width - leftWidth - 6 // borders + padding
	if rightWidth < 20 {
		rightWidth = 20
	}
	// Calculate footer first so we can subtract its height from content
	var footer string
	if m.confirming {
		footer = errorStyle.Render(m.confirmMsg)
	} else if m.filtering {
		footer = m.filterInput.View()
	} else if m.searching {
		footer = m.searchInput.View()
	} else if m.promptMode != promptNone {
		footer = m.promptLabel + "\n" + m.promptInput.View()
	}

	footerHeight := 0
	if footer != "" {
		footerHeight = lipgloss.Height(footer) + 1 // +1 for the newline separator
	}

	contentHeight := m.height - 2 - footerHeight // 1 for status bar, 1 for newline

	if m.showDebug {
		// Split vertically: top half for normal panels, bottom half for debug
		topHeight := contentHeight / 2
		debugHeight := contentHeight - topHeight

		archiveList := m.renderArchiveList(leftWidth, topHeight)
		leftStyle := panelStyle
		if m.activePanel == archivePanel {
			leftStyle = activePanelStyle
		}
		left := leftStyle.Width(leftWidth).Height(topHeight).Render(archiveList)

		detail := m.renderDetail(rightWidth, topHeight)
		rightStyle := panelStyle
		if m.activePanel == detailPanel {
			rightStyle = activePanelStyle
		}
		right := rightStyle.Width(rightWidth).Height(topHeight).Render(detail)

		top := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

		debugWidth := m.width - 4
		debugContent := m.debug.render(debugWidth, debugHeight)
		bottom := panelStyle.Width(debugWidth).Height(debugHeight).Render(debugContent)

		status := renderStatusBar(m.width, len(m.archives), m.loading, m.errMsg, m.statusMsg)

		result := top + "\n" + bottom + "\n" + status
		if footer != "" {
			result += "\n" + footer
		}

		if (m.restoring || m.restoreDone) && !m.confirming {
			popup := m.renderProgressPopup()
			result = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
				lipgloss.WithWhitespaceChars(" "),
			)
		}

		return result
	}

	// Archive list
	archiveList := m.renderArchiveList(leftWidth, contentHeight)
	leftStyle := panelStyle
	if m.activePanel == archivePanel {
		leftStyle = activePanelStyle
	}
	left := leftStyle.Width(leftWidth).Height(contentHeight).Render(archiveList)

	// Detail panel
	detail := m.renderDetail(rightWidth, contentHeight)
	rightStyle := panelStyle
	if m.activePanel == detailPanel {
		rightStyle = activePanelStyle
	}
	right := rightStyle.Width(rightWidth).Height(contentHeight).Render(detail)

	main := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	// Status bar
	status := renderStatusBar(m.width, len(m.archives), m.loading, m.errMsg, m.statusMsg)

	result := main + "\n" + status
	if footer != "" {
		result += "\n" + footer
	}

	if (m.restoring || m.restoreDone) && !m.confirming {
		popup := m.renderProgressPopup()
		result = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	return result
}

func (m Model) renderProgressPopup() string {
	maxWidth := m.width - 10
	if maxWidth > 60 {
		maxWidth = 60
	}

	line := m.restoreProgress
	if line == "" {
		line = "Waiting for data..."
	}
	if len(line) > maxWidth-4 {
		line = line[:maxWidth-7] + "..."
	}

	title := "Restoring..."
	if m.restoreDone {
		title = "Restored!"
	}
	content := lipgloss.JoinVertical(lipgloss.Center,
		titleStyle.Render(title),
		"",
		line,
	)
	return activePanelStyle.Width(maxWidth).Render(content)
}

func (m Model) renderSearchResults() string {
	contentWidth := m.width - 4
	contentHeight := m.height - 4

	var b strings.Builder
	b.WriteString(titleStyle.Render("Search Results"))
	b.WriteString("\n\n")

	if len(m.searchResults) == 0 {
		b.WriteString(dimStyle.Render("No matching files found"))
	} else {
		lines := 0
		maxLines := contentHeight - 4
		for _, sr := range m.searchResults {
			if lines >= maxLines {
				b.WriteString(dimStyle.Render(fmt.Sprintf("\n... more results truncated")))
				break
			}
			b.WriteString(selectedItemStyle.Render(sr.Archive))
			b.WriteString("\n")
			lines++
			for _, f := range sr.Files {
				if lines >= maxLines {
					b.WriteString(dimStyle.Render(fmt.Sprintf("  ... and more files")))
					lines++
					break
				}
				b.WriteString(fmt.Sprintf("  %s\n", truncate(f, contentWidth-4)))
				lines++
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("press esc to close"))

	content := panelStyle.Width(contentWidth).Height(contentHeight).Render(b.String())
	status := renderStatusBar(m.width, len(m.archives), m.loading, m.errMsg, m.statusMsg)
	return content + "\n" + status
}

func (m Model) renderArchiveList(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Archives"))
	b.WriteString("\n\n")

	filtered := m.filteredArchives()
	if len(filtered) == 0 {
		b.WriteString(dimStyle.Render("No archives found"))
		return b.String()
	}

	// Visible window
	visibleStart := 0
	visibleCount := height - 3
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.cursor >= visibleStart+visibleCount {
		visibleStart = m.cursor - visibleCount + 1
	}
	if m.cursor < visibleStart {
		visibleStart = m.cursor
	}

	for i := visibleStart; i < len(filtered) && i < visibleStart+visibleCount; i++ {
		name := truncate(filtered[i].Name, width-4)
		if i == m.cursor {
			b.WriteString(selectedItemStyle.Render("> " + name))
		} else {
			b.WriteString(normalItemStyle.Render("  " + name))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func (m Model) renderDetail(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Detail"))
	b.WriteString("\n\n")

	filtered := m.filteredArchives()
	if len(filtered) == 0 || m.cursor >= len(filtered) {
		b.WriteString(dimStyle.Render("Select an archive"))
		return b.String()
	}

	a := filtered[m.cursor]
	b.WriteString(fmt.Sprintf("Name:  %s\n", a.Name))
	b.WriteString(fmt.Sprintf("Date:  %s\n", a.CreatedAt.Format("2006-01-02 15:04:05")))

	if m.stats != nil && m.stats.ArchiveName == a.Name {
		b.WriteString(fmt.Sprintf("Size:  %s (unique: %s)\n",
			humanBytes(m.stats.TotalSize),
			humanBytes(m.stats.UniqueSize)))
		b.WriteString(fmt.Sprintf("Comp:  %s (unique: %s)\n",
			humanBytes(m.stats.CompressedSize),
			humanBytes(m.stats.UniqueCompSize)))
	}

	if restoredAt, restoredTo := m.cache.GetRestoreInfo(a.Name); !restoredAt.IsZero() {
		b.WriteString(fmt.Sprintf("Last restored: %s → %s\n", restoredAt.Format("2006-01-02 15:04"), restoredTo))
	}

	if m.files != nil {
		b.WriteString(fmt.Sprintf("\nFiles (%d):\n", len(m.files)))
		maxFiles := height - 10
		if maxFiles < 1 {
			maxFiles = 1
		}
		for i, f := range m.files {
			if i >= maxFiles {
				b.WriteString(dimStyle.Render(fmt.Sprintf("  ... and %d more", len(m.files)-maxFiles)))
				break
			}
			prefix := "  "
			if f.IsDir {
				prefix = "  "
			}
			b.WriteString(prefix + truncate(f.Path, width-4) + "\n")
		}
	}

	return b.String()
}

// --- Commands ---

func (m Model) loadArchives() tea.Cmd {
	m.debug.log("loadArchives: checking cache...")
	return func() tea.Msg {
		// Try cache first
		archives, err := m.cache.GetArchives()
		if err == nil && len(archives) > 0 {
			m.debug.log("loadArchives: cache hit, %d archives", len(archives))
			return archivesLoadedMsg{archives}
		}
		m.debug.log("loadArchives: cache miss (err=%v, count=%d), fetching from tarsnap...", err, len(archives))
		// Fetch from tarsnap
		archives, err = m.tarsnap.ListArchives()
		if err != nil {
			m.debug.log("loadArchives: tarsnap error: %v", err)
			return errMsg{err}
		}
		m.debug.log("loadArchives: tarsnap returned %d archives", len(archives))
		sort.Slice(archives, func(i, j int) bool {
			return archives[i].Name < archives[j].Name
		})
		m.cache.PutArchives(archives)
		return archivesLoadedMsg{archives}
	}
}

func (m Model) refreshArchives() tea.Cmd {
	m.debug.log("refreshArchives: clearing stats cache and fetching from tarsnap...")
	return func() tea.Msg {
		m.cache.ClearStats()
		archives, err := m.tarsnap.ListArchives()
		if err != nil {
			m.debug.log("refreshArchives: error: %v", err)
			return errMsg{err}
		}
		m.debug.log("refreshArchives: got %d archives", len(archives))
		sort.Slice(archives, func(i, j int) bool {
			return archives[i].Name < archives[j].Name
		})
		m.cache.PutArchives(archives)
		return archivesLoadedMsg{archives}
	}
}

func (m Model) loadDetail() tea.Cmd {
	filtered := m.filteredArchives()
	if m.cursor >= len(filtered) {
		return nil
	}
	name := filtered[m.cursor].Name
	m.debug.log("loadDetail: loading stats and files for %q", name)

	return tea.Batch(
		func() tea.Msg {
			// Try cache, then fetch
			stats, err := m.cache.GetStats(name)
			if err == nil && stats != nil {
				m.debug.log("loadDetail: stats cache hit for %q", name)
				return statsLoadedMsg{stats}
			}
			m.debug.log("loadDetail: stats cache miss for %q, fetching...", name)
			stats, err = m.tarsnap.ArchiveStats(name)
			if err != nil {
				m.debug.log("loadDetail: stats error for %q: %v", name, err)
				return errMsg{err}
			}
			m.cache.PutStats(stats)
			return statsLoadedMsg{stats}
		},
		func() tea.Msg {
			files, err := m.cache.GetFiles(name)
			if err == nil && len(files) > 0 {
				m.debug.log("loadDetail: files cache hit for %q (%d files)", name, len(files))
				return filesLoadedMsg{files}
			}
			m.debug.log("loadDetail: files cache miss for %q, fetching...", name)
			files, err = m.tarsnap.ListFiles(name)
			if err != nil {
				m.debug.log("loadDetail: files error for %q: %v", name, err)
				return errMsg{err}
			}
			m.cache.PutFiles(name, files)
			return filesLoadedMsg{files}
		},
	)
}

func (m Model) filteredArchives() []tarsnap.Archive {
	if m.filterText == "" {
		return m.archives
	}
	var filtered []tarsnap.Archive
	for _, a := range m.archives {
		if strings.Contains(strings.ToLower(a.Name), strings.ToLower(m.filterText)) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// --- Helpers ---

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
