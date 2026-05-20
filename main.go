package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Aayush9029/cast/internal/cast"
	"github.com/Aayush9029/cast/internal/castui"
	"github.com/Aayush9029/cast/internal/config"
	"github.com/Aayush9029/cast/internal/discovery"
	"github.com/Aayush9029/cast/internal/dlna"
	"github.com/Aayush9029/cast/internal/target"
)

var version = "dev"

const helpText = `cast - stream a video file to a Samsung TV

USAGE
  cast <file-or-url>            cast a local file (or any yt-dlp URL)
  cast discover                 scan for TVs and save one
  cast stop                     stop playback on the current TV
  cast cache                    list cached downloads
  cast clean                    delete cached downloads
  cast --help                   this help

FLAGS
  --tv-ip <addr>                target a specific TV (skip auto-discovery)

The first cast auto-discovers your TV via SSDP. Run 'cast discover' once
when you have multiple TVs to pick a default. Config lives at
~/.config/cast/config.json.

ENVIRONMENT
  CAST_PORT      HTTP file-server port (default 8088)
  CAST_CACHE     cache dir for downloaded files (default /tmp/cast-cache)
  CAST_COOKIES   --cookies-from-browser value for yt-dlp (default safari)
`

func main() {
	if err := run(); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "cast: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("cast", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	tvIP := fs.String("tv-ip", "", "TV IP address (overrides config and discovery)")
	showVer := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVer, "v", false, "print version and exit")
	fs.Usage = func() { fmt.Fprint(os.Stderr, helpText) }
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVer {
		fmt.Printf("cast %s\n", version)
		return nil
	}

	args := fs.Args()
	if len(args) == 0 {
		fmt.Print(helpText)
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(helpText)
		return nil
	case "-v", "--version", "version":
		fmt.Printf("cast %s\n", version)
		return nil
	case "discover":
		return cmdDiscover(ctx)
	case "cache":
		return cmdCache()
	case "clean":
		return cmdClean()
	}

	tv, err := target.Resolve(ctx, *tvIP)
	if err != nil {
		return err
	}

	switch args[0] {
	case "stop":
		_, err := dlna.New(tv.IP).Stop(ctx)
		return err
	default:
		return cmdCast(ctx, tv, args[0])
	}
}

func cmdDiscover(ctx context.Context) error {
	fmt.Fprintln(os.Stderr, "scanning...")
	devs, err := discovery.Discover(ctx, 4*time.Second)
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		return errors.New("no Samsung TVs found on this network")
	}
	pick := devs[0]
	if len(devs) > 1 {
		fmt.Fprintln(os.Stderr, "found:")
		for i, d := range devs {
			fmt.Fprintf(os.Stderr, "  [%d] %s  %s  %s\n", i+1, d.FriendlyName, d.Model, d.IP)
		}
		fmt.Fprint(os.Stderr, "pick (1-", len(devs), "): ")
		var n int
		if _, err := fmt.Scanln(&n); err != nil {
			return fmt.Errorf("read selection: %w", err)
		}
		if n < 1 || n > len(devs) {
			return fmt.Errorf("out of range")
		}
		pick = devs[n-1]
	}
	if err := config.Save(config.Config{
		TVIP:             pick.IP,
		TVName:           pick.FriendlyName,
		TVModel:          pick.Model,
		LastDiscoveredAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("saved: %s  %s  %s\n", pick.FriendlyName, pick.Model, pick.IP)
	return nil
}

func cmdCache() error {
	d := cast.CacheDir()
	matches, err := filepath.Glob(filepath.Join(d, "*.mp4"))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		fmt.Printf("%s (empty)\n", d)
		return nil
	}
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil {
			continue
		}
		fmt.Printf("  %5d MB  %s\n", st.Size()/1024/1024, filepath.Base(m))
	}
	return nil
}

func cmdClean() error {
	d := cast.CacheDir()
	for _, pattern := range []string{"*.mp4", "*.part"} {
		matches, _ := filepath.Glob(filepath.Join(d, pattern))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
	fmt.Printf("cleaned %s\n", d)
	return nil
}

func cmdCast(ctx context.Context, tv target.TV, src string) error {
	if isTerminal(os.Stdout) {
		return castui.Run(ctx, castui.Params{TV: tv, Source: src, HTTPPort: httpPort()})
	}
	return castHeadless(ctx, tv, src)
}

func castHeadless(ctx context.Context, tv target.TV, src string) error {
	path, title, err := resolveSource(ctx, src)
	if err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	size := st.Size()

	ip, err := cast.MyLANIP(tv.IP)
	if err != nil {
		return err
	}
	streamURL := fmt.Sprintf("http://%s:%s/%s", ip, httpPort(), filepath.Base(path))
	fmt.Fprintf(os.Stderr, "[serve] %s  (%d MB)\n", streamURL, size/1024/1024)

	listener, err := net.Listen("tcp", "0.0.0.0:"+httpPort())
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", &cast.FileHandler{Path: path})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "[http] %v\n", err)
		}
	}()

	avt := dlna.New(tv.IP)
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, _ = avt.Stop(stopCtx)
	cancel()
	time.Sleep(300 * time.Millisecond)

	setCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := avt.SetAVTransportURI(setCtx, streamURL, title, size); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)

	playCtx, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	if _, err := avt.Play(playCtx); err != nil {
		return err
	}
	fmt.Printf("\nPlaying: %s\n  File: %s\n  Ctrl-C to stop\n\n", title, path)

	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "\nstopping...")
	stopCtx2, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	if _, err := avt.Stop(stopCtx2); err != nil {
		fmt.Fprintf(os.Stderr, "stop: %v\n", err)
	}
	shutdownCtx, cancel4 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel4()
	_ = srv.Shutdown(shutdownCtx)
	return nil
}

func resolveSource(ctx context.Context, src string) (path, title string, err error) {
	if st, err := os.Stat(src); err == nil && !st.IsDir() {
		return src, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)), nil
	}
	if _, err := cast.EnsureCacheDir(); err != nil {
		return "", "", err
	}
	key := cast.CacheKey(ctx, src)
	if p, ok := cast.HasUsable(key); ok {
		return p, prettyTitle(key), nil
	}
	out := cast.CachedPath(key)
	if err := cast.Download(ctx, src, out, os.Stderr); err != nil {
		return "", "", err
	}
	return out, prettyTitle(key), nil
}

func prettyTitle(key string) string {
	if i := strings.Index(key, "_"); i >= 0 {
		key = key[i+1:]
	}
	return strings.ReplaceAll(key, "_", " ")
}

func httpPort() string {
	if v := os.Getenv("CAST_PORT"); v != "" {
		return v
	}
	return "8088"
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}
