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
	"math"
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
	"github.com/Aayush9029/cast/internal/localaudio"
	"github.com/Aayush9029/cast/internal/target"
	"github.com/Aayush9029/cast/internal/tui"
)

// Params configures Run. Source is either a URL or a local file path; HTTPPort
// is the listen port (default already resolved by the caller).
type Params struct {
	TV       target.TV
	Source   string
	HTTPPort string

	// LocalAudio routes audio to this Mac (speakers/AirPods) while video plays
	// on the TV: the TV is muted and ffplay plays the file's audio track in
	// sync. AudioDelay offsets local audio relative to the TV (positive delays
	// audio to match a TV that buffers video behind).
	LocalAudio bool
	AudioDelay time.Duration
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
	rc          *dlna.RenderingControl
	pollEvery   time.Duration
	stopRequest bool
	targetRetry bool

	player     *localaudio.Player
	audioDelay time.Duration
	audioDrift time.Duration
	syncEvery  time.Duration

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
type rediscoverDoneMsg struct {
	tv  target.TV
	err error
}
type controlDoneMsg struct {
	status    string
	transport string
	err       error
}
type stoppedMsg struct{}
type audioSyncMsg struct {
	drift time.Duration
	err   error
}

func newModel(ctx context.Context, p Params) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(tui.ColorAccent)

	pr := progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage())
	pr.Width = 50

	avt := dlna.New(p.TV.IP)

	m := model{
		ctx:        ctx,
		p:          p,
		spinner:    s,
		prog:       pr,
		avt:        avt,
		rc:         dlna.NewRenderingControl(p.TV.IP),
		pollEvery:  2 * time.Second,
		audioDelay: p.AudioDelay,
		syncEvery:  time.Second,
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
		streamURL, err := streamURLFor(path, tvIP, port)
		if err != nil {
			return playbackErrMsg{err: fmt.Errorf("lan ip: %w", err)}
		}
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

func streamURLFor(path, tvIP, port string) (string, error) {
	ip, err := cast.MyLANIP(tvIP)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s:%s/%s", ip, port, filepath.Base(path)), nil
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
		if state := dlna.CurrentTransportState(resp); state != "" {
			return stateMsg{state: state}
		}
		return stateMsg{state: "?"}
	})
}

func rediscoverTargetCmd(ctx context.Context, tv target.TV) tea.Cmd {
	return func() tea.Msg {
		fresh, err := target.Rediscover(ctx, tv)
		return rediscoverDoneMsg{tv: fresh, err: err}
	}
}

func togglePlayPauseCmd(ctx context.Context, avt *dlna.Client) tea.Cmd {
	return func() tea.Msg {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		resp, err := avt.GetTransportInfo(callCtx)
		if err != nil {
			return controlDoneMsg{err: err}
		}
		if dlna.CurrentTransportState(resp) == "PLAYING" {
			if _, err := avt.Pause(callCtx); err != nil {
				return controlDoneMsg{err: err}
			}
			return controlDoneMsg{status: "paused", transport: "PAUSED_PLAYBACK"}
		}
		if _, err := avt.Play(callCtx); err != nil {
			return controlDoneMsg{err: err}
		}
		return controlDoneMsg{status: "playing", transport: "PLAYING"}
	}
}

func seekCmd(ctx context.Context, avt *dlna.Client, delta time.Duration) tea.Cmd {
	return func() tea.Msg {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, err := avt.SeekRelative(callCtx, delta); err != nil {
			return controlDoneMsg{err: err}
		}
		if delta < 0 {
			return controlDoneMsg{status: "skipped back 15s"}
		}
		return controlDoneMsg{status: "skipped ahead 15s"}
	}
}

func volumeCmd(ctx context.Context, rc *dlna.RenderingControl, delta int) tea.Cmd {
	return func() tea.Msg {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		volume, err := rc.AdjustVolume(callCtx, delta)
		if err != nil {
			return controlDoneMsg{err: err}
		}
		return controlDoneMsg{status: fmt.Sprintf("volume %d", volume)}
	}
}

// audioSyncCmd polls the TV's transport state and position, then nudges the
// local audio player toward tvPos+offset. When the TV is not PLAYING the
// player is stopped so it resyncs cleanly on resume.
func audioSyncCmd(ctx context.Context, avt *dlna.Client, player *localaudio.Player, offset, every time.Duration) tea.Cmd {
	return tea.Tick(every, func(_ time.Time) tea.Msg {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if ti, err := avt.GetTransportInfo(callCtx); err == nil && dlna.CurrentTransportState(ti) != "PLAYING" {
			player.Stop()
			return audioSyncMsg{}
		}
		pos, err := avt.Position(callCtx)
		if err != nil {
			return audioSyncMsg{err: err}
		}
		drift, _, serr := player.Sync(pos, offset)
		return audioSyncMsg{drift: drift, err: serr}
	})
}

// nudgeAudioCmd reseeks the local player immediately to the TV position plus
// the new offset, so delay tweaks take effect without waiting for the next
// drift tick.
func nudgeAudioCmd(ctx context.Context, avt *dlna.Client, player *localaudio.Player, offset time.Duration) tea.Cmd {
	return func() tea.Msg {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		pos, err := avt.Position(callCtx)
		if err != nil {
			return controlDoneMsg{err: err}
		}
		if err := player.PlayFrom(pos + offset); err != nil {
			return controlDoneMsg{err: err}
		}
		return controlDoneMsg{status: fmt.Sprintf("audio delay %s", offset)}
	}
}

func muteTVCmd(ctx context.Context, rc *dlna.RenderingControl, mute bool) tea.Cmd {
	return func() tea.Msg {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := rc.SetMute(callCtx, mute); err != nil {
			return controlDoneMsg{err: err}
		}
		if mute {
			return controlDoneMsg{status: "TV muted — audio on this Mac"}
		}
		return controlDoneMsg{status: "TV unmuted"}
	}
}

func stopOnTVCmd(avt *dlna.Client, rc *dlna.RenderingControl, srv *http.Server, player *localaudio.Player) tea.Cmd {
	return func() tea.Msg {
		if player != nil {
			player.Stop()
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = avt.Stop(stopCtx)
		if player != nil && rc != nil {
			unmuteCtx, uc := context.WithTimeout(context.Background(), 5*time.Second)
			_ = rc.SetMute(unmuteCtx, false)
			uc()
		}
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
		cmds := []tea.Cmd{
			playOnTVCmd(m.ctx, m.avt, m.streamURL, m.title, size),
			pollStateCmd(m.ctx, m.avt, m.pollEvery),
		}
		if m.p.LocalAudio {
			m.player = localaudio.New(m.cachePath)
			cmds = append(cmds,
				muteTVCmd(m.ctx, m.rc, true),
				audioSyncCmd(m.ctx, m.avt, m.player, m.audioDelay, m.syncEvery),
			)
		}
		return m, tea.Batch(cmds...)

	case audioSyncMsg:
		if m.phase != phaseServing || m.player == nil {
			return m, nil
		}
		if msg.err == nil {
			m.audioDrift = msg.drift
		}
		return m, audioSyncCmd(m.ctx, m.avt, m.player, m.audioDelay, m.syncEvery)

	case stateMsg:
		if m.phase != phaseServing {
			return m, nil
		}
		m.transport = msg.state
		return m, pollStateCmd(m.ctx, m.avt, m.pollEvery)

	case playbackErrMsg:
		if !m.targetRetry && m.p.TV.Source != "flag" && shouldRescanPlaybackError(msg.err) {
			m.targetRetry = true
			m.transport = "rescanning..."
			m.appendLog("TV stopped responding; rescanning...")
			return m, rediscoverTargetCmd(m.ctx, m.p.TV)
		}
		m.phase = phaseError
		m.err = msg.err.Error()
		return m, nil

	case rediscoverDoneMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.err = "playback failed, and auto-rescan found no replacement TV: " + msg.err.Error()
			return m, nil
		}
		oldIP := m.p.TV.IP
		m.p.TV = msg.tv
		m.avt = dlna.New(msg.tv.IP)
		m.rc = dlna.NewRenderingControl(msg.tv.IP)
		streamURL, err := streamURLFor(m.cachePath, msg.tv.IP, m.p.HTTPPort)
		if err != nil {
			m.phase = phaseError
			m.err = err.Error()
			return m, nil
		}
		m.streamURL = streamURL
		if msg.tv.IP != oldIP {
			m.appendLog("found TV at " + msg.tv.IP)
		}
		var size int64
		if st, err := os.Stat(m.cachePath); err == nil {
			size = st.Size()
		}
		cmds := []tea.Cmd{
			playOnTVCmd(m.ctx, m.avt, m.streamURL, m.title, size),
			pollStateCmd(m.ctx, m.avt, m.pollEvery),
		}
		if m.p.LocalAudio && m.player != nil {
			cmds = append(cmds,
				muteTVCmd(m.ctx, m.rc, true),
				audioSyncCmd(m.ctx, m.avt, m.player, m.audioDelay, m.syncEvery),
			)
		}
		return m, tea.Batch(cmds...)

	case controlDoneMsg:
		if msg.err != nil {
			if !m.targetRetry && m.p.TV.Source != "flag" && shouldRescanPlaybackError(msg.err) {
				m.targetRetry = true
				m.transport = "rescanning..."
				m.appendLog("TV stopped responding; rescanning...")
				return m, rediscoverTargetCmd(m.ctx, m.p.TV)
			}
			m.appendLog("control failed: " + msg.err.Error())
			return m, nil
		}
		if msg.transport != "" {
			m.transport = msg.transport
		}
		if msg.status != "" {
			m.appendLog(msg.status)
		}
		return m, nil

	case stoppedMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.phase == phaseServing && !m.stopRequest {
				m.stopRequest = true
				return m, stopOnTVCmd(m.avt, m.rc, m.server, m.player)
			}
			return m, tea.Quit
		case "s":
			if m.phase == phaseServing && !m.stopRequest {
				m.stopRequest = true
				return m, stopOnTVCmd(m.avt, m.rc, m.server, m.player)
			}
		case " ", "space":
			if m.phase == phaseServing {
				return m, togglePlayPauseCmd(m.ctx, m.avt)
			}
		case "up":
			if m.phase == phaseServing {
				return m, volumeCmd(m.ctx, m.rc, 5)
			}
		case "down":
			if m.phase == phaseServing {
				return m, volumeCmd(m.ctx, m.rc, -5)
			}
		case "left":
			if m.phase == phaseServing {
				return m, seekCmd(m.ctx, m.avt, -15*time.Second)
			}
		case "right":
			if m.phase == phaseServing {
				return m, seekCmd(m.ctx, m.avt, 15*time.Second)
			}
		case "[":
			if m.phase == phaseServing && m.player != nil {
				m.audioDelay -= 25 * time.Millisecond
				return m, nudgeAudioCmd(m.ctx, m.avt, m.player, m.audioDelay)
			}
		case "]":
			if m.phase == phaseServing && m.player != nil {
				m.audioDelay += 25 * time.Millisecond
				return m, nudgeAudioCmd(m.ctx, m.avt, m.player, m.audioDelay)
			}
		case "{":
			if m.phase == phaseServing && m.player != nil {
				m.audioDelay -= 250 * time.Millisecond
				return m, nudgeAudioCmd(m.ctx, m.avt, m.player, m.audioDelay)
			}
		case "}":
			if m.phase == phaseServing && m.player != nil {
				m.audioDelay += 250 * time.Millisecond
				return m, nudgeAudioCmd(m.ctx, m.avt, m.player, m.audioDelay)
			}
		case "(":
			if m.phase == phaseServing && m.player != nil {
				m.audioDelay -= 10 * time.Millisecond
				return m, nudgeAudioCmd(m.ctx, m.avt, m.player, m.audioDelay)
			}
		case ")":
			if m.phase == phaseServing && m.player != nil {
				m.audioDelay += 10 * time.Millisecond
				return m, nudgeAudioCmd(m.ctx, m.avt, m.player, m.audioDelay)
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
		rows := []string{
			title,
			urlBox,
			tui.HintMuted.Render("TV state: ") + tui.StatusOK.Render(state),
		}
		if m.p.LocalAudio {
			rows = append(rows,
				tui.HintMuted.Render("Audio → ")+tui.StatusOK.Render("this Mac")+
					tui.HintMuted.Render(fmt.Sprintf("   (drift %s)", m.audioDrift.Round(time.Millisecond))),
				renderSyncBar(m.audioDelay),
			)
		}
		rows = append(rows, "", m.logView())
		body = lipgloss.JoinVertical(lipgloss.Left, rows...)
	case phaseDone:
		body = tui.Panel.Width(60).Render(tui.StatusOK.Render("done"))
	case phaseError:
		body = tui.Panel.Width(70).Render(
			tui.StatusBad.Render("error") + "\n\n" + m.err)
	}

	hints := []tui.HintItem{
		{Key: "↑/↓", Label: "volume"},
		{Key: "←/→", Label: "seek 15s"},
		{Key: "space", Label: "play/pause"},
	}
	if m.p.LocalAudio {
		hints = append(hints, tui.HintItem{Key: "[/]", Label: "sound ±25ms"})
		hints = append(hints, tui.HintItem{Key: "(/)", Label: "±10ms"})
		hints = append(hints, tui.HintItem{Key: "{/}", Label: "±250ms"})
	}
	hints = append(hints,
		tui.HintItem{Key: "s", Label: "stop"},
		tui.HintItem{Key: "q", Label: "quit"},
	)
	footer := tui.Footer.Render(tui.FormatHints(hints))
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// syncBarSpan is the half-range the slider maps; delays beyond it clamp to the
// ends. syncBarHalf is the cell count on each side of the centre tick.
const (
	syncBarSpan = 1000 * time.Millisecond
	syncBarHalf = 16
)

// renderSyncBar draws a centred A/V-sync slider: a ● marker that slides left
// (sound earlier than the picture, blue) or right (sound later, amber) from the
// in-sync centre tick, plus a plain-English caption. It replaces the cryptic
// "delay ±Nms" readout so the direction of a nudge is obvious at a glance.
func renderSyncBar(delay time.Duration) string {
	d := delay
	if d > syncBarSpan {
		d = syncBarSpan
	} else if d < -syncBarSpan {
		d = -syncBarSpan
	}
	pos := syncBarHalf + int(math.Round(float64(d)/float64(syncBarSpan)*float64(syncBarHalf)))
	width := syncBarHalf*2 + 1

	earlier := lipgloss.NewStyle().Foreground(tui.ColorAccent)
	later := lipgloss.NewStyle().Foreground(tui.ColorActive)
	rail := lipgloss.NewStyle().Foreground(tui.ColorBorder)
	synced := lipgloss.NewStyle().Foreground(tui.ColorOK).Bold(true)

	var b strings.Builder
	b.WriteString(earlier.Render("◀"))
	for i := 0; i < width; i++ {
		switch {
		case i == pos && pos == syncBarHalf:
			b.WriteString(synced.Render("●"))
		case i == pos && pos < syncBarHalf:
			b.WriteString(earlier.Render("●"))
		case i == pos:
			b.WriteString(later.Render("●"))
		case i == syncBarHalf:
			b.WriteString(rail.Render("┊"))
		case i > pos && i < syncBarHalf:
			b.WriteString(earlier.Render("─"))
		case i < pos && i > syncBarHalf:
			b.WriteString(later.Render("─"))
		default:
			b.WriteString(rail.Render("─"))
		}
	}
	b.WriteString(later.Render("▶"))

	labels := tui.HintMuted.Render(
		"sound earlier" + strings.Repeat(" ", width-len("sound earlier")-len("sound later")) + "sound later")
	return lipgloss.JoinVertical(lipgloss.Left,
		" "+labels,
		b.String(),
		" "+tui.HintMuted.Render(delayCaption(d)),
	)
}

func delayCaption(d time.Duration) string {
	ms := d.Round(time.Millisecond)
	switch {
	case ms == 0:
		return "in sync"
	case ms > 0:
		return fmt.Sprintf("sound plays %s after the picture", ms)
	default:
		return fmt.Sprintf("sound plays %s before the picture", -ms)
	}
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

func shouldRescanPlaybackError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "soap ") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "connect:") ||
		strings.Contains(msg, "host is down") ||
		strings.Contains(msg, "network is unreachable") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "i/o timeout")
}
