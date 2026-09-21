// Command breaklist generates thermal-printer morning reports and serves the
// configuration GUI.
//
//	breaklist serve              start the GUI + scheduler (default)
//	breaklist generate [--print] build the report once and exit
//	breaklist modules            list available sections
//	breaklist open               open the GUI in the browser (starts serve)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"breaklist/internal/config"
	"breaklist/internal/logging"
	"breaklist/internal/modules"
	"breaklist/internal/render"
	"breaklist/internal/server"
)

var version = "2.0.0"

func main() {
	log.SetFlags(log.Ltime)
	server.Version = version

	fs := flag.NewFlagSet("breaklist", flag.ExitOnError)
	dataDir := fs.String("data", "", "data folder holding breaklist.json, lists and output (default: folder of the executable, or current dir if it has breaklist.json)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Breaklist %s — morning reports for thermal printers\n\n", version)
		fmt.Fprintln(os.Stderr, "Usage: breaklist [--data DIR] <command>")
		fmt.Fprintln(os.Stderr, "  serve      start the web GUI and scheduler (default)")
		fmt.Fprintln(os.Stderr, "  open       start the GUI and open it in your browser")
		fmt.Fprintln(os.Stderr, "  generate   build the report once (add --print to send it to the printer)")
		fmt.Fprintln(os.Stderr, "  modules    list the available sections")
		fmt.Fprintln(os.Stderr, "  version    print the version")
	}
	// Allow flags before or after the command.
	var args []string
	var cmd string
	for _, a := range os.Args[1:] {
		if cmd == "" && !strings.HasPrefix(a, "-") {
			cmd = a
			continue
		}
		args = append(args, a)
	}
	_ = fs.Parse(args)
	if cmd == "" {
		cmd = "serve"
	}

	dir := resolveDataDir(*dataDir)
	if err := logging.Setup(dir); err != nil {
		log.Printf("warning: could not open log file: %v", err)
	}
	log.Printf("Breaklist %s starting (%s) · data folder %s", version, cmd, dir)
	store := config.NewStore(dir)
	cfg, err := store.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}
	fresh := false
	if _, statErr := os.Stat(store.Path()); os.IsNotExist(statErr) {
		fresh = true
	}
	modules.NormalizeSections(cfg)
	if err := store.Save(cfg); err != nil {
		log.Fatalf("saving config: %v", err)
	}
	if fresh {
		log.Printf("created %s (imported legacy .env settings if any were found)", store.Path())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "version":
		fmt.Println(version)
	case "modules":
		for _, info := range modules.Catalog() {
			fmt.Printf("%-24s %-22s %s\n", info.ID, info.Category, info.Name)
		}
	case "generate":
		doPrint := false
		for _, a := range fs.Args() {
			if a == "--print" || a == "-print" {
				doPrint = true
			}
		}
		r := render.New(dir)
		res, err := r.Generate(ctx, store.Get(), nil)
		if res != nil {
			for _, l := range res.Log {
				log.Print(l)
			}
		}
		if err != nil {
			log.Fatalf("generate failed: %v", err)
		}
		if doPrint {
			if out, err := r.Print(ctx, store.Get()); err != nil {
				log.Fatalf("print failed: %v", err)
			} else {
				log.Printf("sent to printer %s", out)
			}
		}
	case "serve", "open":
		srv := server.New(store, dir)
		if cmd == "open" {
			go func() {
				time.Sleep(700 * time.Millisecond)
				openBrowser(fmt.Sprintf("http://127.0.0.1:%d", store.Get().Server.Port))
			}()
		}
		if err := srv.Run(ctx); err != nil {
			log.Fatal(err)
		}
	default:
		fs.Usage()
		os.Exit(2)
	}
}

// resolveDataDir picks the folder that holds breaklist.json: an explicit flag,
// else the current directory if it already has a config or legacy .env, else
// the executable's folder.
func resolveDataDir(flagDir string) string {
	if flagDir != "" {
		abs, _ := filepath.Abs(flagDir)
		return abs
	}
	cwd, _ := os.Getwd()
	for _, name := range []string{config.FileName, ".env", "reportGenerator"} {
		if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
			return cwd
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return cwd
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
