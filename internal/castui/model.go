// Package castui drives the Bubble Tea progress TUI for `tv-cast <url-or-path>`.
//
// Three phases:
//  1. resolving via yt-dlp probe       (spinner)
//  2. downloading via yt-dlp           (progress bar fed by ParseProgress)
//  3. serving + playing                (URL box + GetTransportInfo poll)
//
// Local-file paths skip phases 1 and 2 entirely. URL casts walk all three.
// On quit/`s`, a Stop SOAP is sent to the TV and the HTTP file server is
// shut down.
package castui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Aayush9029/cast/internal/cast"
	"github.com/Aayush9029/cast/internal/dlna"
	"github.com/Aayush9029/cast/internal/target"
	"github.com/Aayush9029/cast/internal/tui"
)

// Params configures Run. Source is either a URL or a local file path; HTTPPort
// is the listen port (default already resolved by the caller).
type Params struct {
	TV       target.TV
	Source   string
	HTTPPort string
}

// Run mounts the TUI and blocks until the user quits or the source fails.
func Run(ctx context.Context, p Params) error {
	m := newModel(ctx, p)
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := prog.Run()
	return err
}

type phase int

const (
	phaseResolving phase = iota
	phaseDownloading
	phaseServing
	phaseDone
	phaseError
)

type model struct {
	ctx context.Context
	p   Params

	phase phase

	spinner spinner.Model
	prog    progress.Model

	title       string
	cachePath   string
	streamURL   string
	transport   string
	err         string
	logs        []string
	pctSize     string
	pctSpeed    string
	pctETA      string
	pctValue    float64
	server      *http.Server
	avt         *dlna.Client
	pollEvery   time.Duration
	stopRequest bool

	dlCh chan string

	w, h int
}

// --- messages ---

type resolveDoneMsg struct {
	path  string
	title string
	err   error
}
type downloadLineMsg struct{ line string }
type downloadDoneMsg struct{ err error }
type servingStartedMsg struct {
	streamURL string
	server    *http.Server
}
type stateMsg struct{ state string }
type tickMsg time.Time
type playbackErrMsg struct{ err error }
type stoppedMsg struct{}

func newModel(ctx context.Context, p Params) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(tui.ColorAccent)

	pr := progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage())
	pr.Width = 50

	avt := dlna.New(p.TV.IP)

	m := model{
		ctx:       ctx,
		p:         p,
		spinner:   s,
		prog:      pr,
		avt:       avt,
		pollEvery: 2 * time.Second,
	}

	if isLocalFile(p.Source) {
		m.phase = phaseServing
		m.title = strings.TrimSuffix(filepath.Base(p.Source), filepath.Ext(p.Source))
		m.cachePath = p.Source
	} else {
		m.phase = phaseResolving
	}
	return m
}

func isLocalFile(s string) bool {
	if st, err := os.Stat(s); err == nil && !st.IsDir() {
		return true
	}
	return false
}

// --- commands ---

func resolveCmd(ctx context.Context, src string) tea.Cmd {
	return func() tea.Msg {
		_, _ = cast.EnsureCacheDir()
		key := cast.CacheKey(ctx, src)
		title := strings.ReplaceAll(keyTitle(key), "_", " ")
		if p, ok := cast.HasUsable(key); ok {
			return resolveDoneMsg{path: p, title: title}
		}
		out := cast.CachedPath(key)
		// Returning a resolveDone with no error tells the model to kick off
		// the download phase with a fresh cachePath.
		return resolveDoneMsg{path: out, title: title}
	}
}

func downloadCmd(ctx context.Context, src, outPath string, ch chan<- string) tea.Cmd {
	return func() tea.Msg {
		err := cast.DownloadLines(ctx, src, outPath, func(line string) {
			select {
			case ch <- line:
			default:
			}
		})
		close(ch)
		return downloadDoneMsg{err: err}
	}
}

func waitLineCmd(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return downloadLineMsg{line: line}
	}
}

func startServerCmd(ctx context.Context, path, tvIP, port string) tea.Cmd {
	return func() tea.Msg {
		ip, err := cast.MyLANIP(tvIP)
		if err != nil {
			return playbackErrMsg{err: fmt.Errorf("lan ip: %w", err)}
		}
		streamURL := fmt.Sprintf("http://%s:%s/%s", ip, port, filepath.Base(path))
		listener, err := net.Listen("tcp", "0.0.0.0:"+port)
		if err != nil {
			return playbackErrMsg{err: fmt.Errorf("listen: %w", err)}
		}
		mux := http.NewServeMux()
		mux.Handle("/", &cast.FileHandler{Path: path})
		srv := &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 30 * time.Second,
		}
		go func() { _ = srv.Serve(listener) }()
		return servingStartedMsg{streamURL: streamURL, server: srv}
	}
}

func playOnTVCmd(ctx context.Context, avt *dlna.Client, streamURL, title string, size int64) tea.Cmd {
	return func() tea.Msg {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = avt.Stop(stopCtx)
		cancel()
		time.Sleep(300 * time.Millisecond)

		setCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := avt.SetAVTransportURI(setCtx, streamURL, title, size); err != nil {
			return playbackErrMsg{err: err}
		}
		time.Sleep(500 * time.Millisecond)

		playCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := avt.Play(playCtx); err != nil {
			return playbackErrMsg{err: err}
		}
		return stateMsg{state: "PLAYING"}
	}
}

func pollStateCmd(ctx context.Context, avt *dlna.Client, every time.Duration) tea.Cmd {
	return tea.Tick(every, func(_ time.Time) tea.Msg {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		resp, err := avt.GetTransportInfo(callCtx)
		if err != nil {
			return stateMsg{state: "?"}
		}
		// Look for <CurrentTransportState>X</CurrentTransportState>.
		if i := strings.Index(resp, "<CurrentTransportState>"); i >= 0 {
			rest := resp[i+len("<CurrentTransportState>"):]
			if j := strings.Index(rest, "</CurrentTransportState>"); j >= 0 {
				return stateMsg{state: rest[:j]}
			}
		}
		return stateMsg{state: "?"}
	})
}

func stopOnTVCmd(avt *dlna.Client, srv *http.Server) tea.Cmd {
	return func() tea.Msg {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = avt.Stop(stopCtx)
		if srv != nil {
			shutCtx, sc := context.WithTimeout(context.Background(), 3*time.Second)
			defer sc()
			_ = srv.Shutdown(shutCtx)
		}
		return stoppedMsg{}
	}
}

func tick() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// --- lifecycle wiring ---

func (m model) Init() tea.Cmd {
	if m.phase == phaseServing {
		return tea.Batch(m.spinner.Tick, m.startServingChain(), tick())
	}
	return tea.Batch(m.spinner.Tick, resolveCmd(m.ctx, m.p.Source), tick())
}

// startServingChain triggers the HTTP listener; once it returns we issue
// SetAVTransportURI+Play, then start polling GetTransportInfo.
func (m model) startServingChain() tea.Cmd {
	return startServerCmd(m.ctx, m.cachePath, m.p.TV.IP, m.p.HTTPPort)
}

// --- update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		barWidth := msg.Width - 20
		if barWidth < 20 {
			barWidth = 20
		}
		if barWidth > 80 {
			barWidth = 80
		}
		m.prog.Width = barWidth
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		return m, tick()

	case resolveDoneMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.err = msg.err.Error()
			return m, nil
		}
		m.title = msg.title
		m.cachePath = msg.path
		// Cache hit?
		if st, err := os.Stat(msg.path); err == nil && st.Size() > cast.MinUsable {
			m.appendLog(fmt.Sprintf("cache hit: %s (%d MB)", filepath.Base(msg.path), st.Size()/1024/1024))
			m.phase = phaseServing
			return m, m.startServingChain()
		}
		// Need to download.
		m.phase = phaseDownloading
		m.dlCh = make(chan string, 32)
		return m, tea.Batch(
			downloadCmd(m.ctx, m.p.Source, msg.path, m.dlCh),
			waitLineCmd(m.dlCh),
		)

	case downloadLineMsg:
		parsed, _ := ParseProgress(msg.line)
		if parsed.Stage == "downloading" && parsed.Percent > 0 {
			m.pctValue = parsed.Percent / 100.0
			m.pctSize = parsed.Size
			m.pctSpeed = parsed.Speed
			m.pctETA = parsed.ETA
		} else if parsed.Stage == "destination" {
			m.appendLog("→ " + parsed.Raw)
		} else if parsed.Stage == "merging" {
			m.appendLog(parsed.Raw)
		} else if parsed.Raw != "" {
			m.appendLog(parsed.Raw)
		}
		// Re-arm reader.
		return m, waitLineCmd(m.dlCh)

	case downloadDoneMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.err = msg.err.Error()
			return m, nil
		}
		m.phase = phaseServing
		return m, m.startServingChain()

	case servingStartedMsg:
		m.server = msg.server
		m.streamURL = msg.streamURL
		var size int64
		if st, err := os.Stat(m.cachePath); err == nil {
			size = st.Size()
		}
		return m, tea.Batch(
			playOnTVCmd(m.ctx, m.avt, m.streamURL, m.title, size),
			pollStateCmd(m.ctx, m.avt, m.pollEvery),
		)

	case stateMsg:
		m.transport = msg.state
		return m, pollStateCmd(m.ctx, m.avt, m.pollEvery)

	case playbackErrMsg:
		m.phase = phaseError
		m.err = msg.err.Error()
		return m, nil

	case stoppedMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.phase == phaseServing && !m.stopRequest {
				m.stopRequest = true
				return m, stopOnTVCmd(m.avt, m.server)
			}
			return m, tea.Quit
		case "s":
			if m.phase == phaseServing && !m.stopRequest {
				m.stopRequest = true
				return m, stopOnTVCmd(m.avt, m.server)
			}
		}
	}
	return m, nil
}

func (m *model) appendLog(s string) {
	m.logs = append(m.logs, s)
	if len(m.logs) > 8 {
		m.logs = m.logs[len(m.logs)-8:]
	}
}

// --- view ---

func (m model) View() string {
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		tui.Header.Render("cast"),
		"  ",
		tui.HintMuted.Render("→  "+m.p.TV.Banner()),
	)

	var body string
	switch m.phase {
	case phaseResolving:
		body = tui.Panel.Width(70).Render(
			m.spinner.View() + "  " + tui.StatusPending.Render("Resolving source via yt-dlp…") +
				"\n\n" + tui.HintMuted.Render(m.p.Source))
	case phaseDownloading:
		bar := m.prog.ViewAs(m.pctValue)
		meta := fmt.Sprintf("%5.1f%%  %s  @ %s  ETA %s",
			m.pctValue*100, m.pctSize, m.pctSpeed, m.pctETA)
		title := tui.TitleBar.Render("Downloading " + m.title)
		body = tui.Panel.Width(80).Render(
			title + "\n\n" + bar + "\n" + tui.HintMuted.Render(meta) +
				"\n\n" + m.logView())
	case phaseServing:
		state := m.transport
		if state == "" {
			state = "starting…"
		}
		title := tui.TitleBar.Render("Serving " + m.title)
		urlBox := tui.Panel.Render(tui.HintMuted.Render("URL: ") + m.streamURL)
		body = lipgloss.JoinVertical(lipgloss.Left,
			title,
			urlBox,
			tui.HintMuted.Render("TV state: ")+tui.StatusOK.Render(state),
			"",
			m.logView(),
		)
	case phaseDone:
		body = tui.Panel.Width(60).Render(tui.StatusOK.Render("done"))
	case phaseError:
		body = tui.Panel.Width(70).Render(
			tui.StatusBad.Render("error") + "\n\n" + m.err)
	}

	footer := tui.Footer.Render(tui.FormatHints([]tui.HintItem{
		{Key: "s", Label: "stop"},
		{Key: "q", Label: "quit"},
	}))
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m model) logView() string {
	if len(m.logs) == 0 {
		return ""
	}
	return tui.HintMuted.Render(strings.Join(m.logs, "\n"))
}

func keyTitle(key string) string {
	if i := strings.Index(key, "_"); i >= 0 {
		return key[i+1:]
	}
	return key
}
