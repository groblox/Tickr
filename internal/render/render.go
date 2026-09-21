// Package render assembles module sections into an HTML page and drives
// wkhtmltopdf to produce a single-page PDF sized for the receipt printer.
package render

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"html"
	"html/template"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"breaklist/internal/config"
	"breaklist/internal/modules"
)

// printerLeftMarginMM reserves space many receipt printer drivers won't
// print into. It is added on top of the configured paper width so the
// content area itself keeps its full size.
const printerLeftMarginMM = 3.0

// contentDPI is wkhtmltopdf/wkhtmltoimage's actual, fixed CSS-px-per-inch
// rendering rate (confirmed empirically: --dpi has no effect on it). It
// must stay 96 — this is what makes a body declared N px wide occupy
// exactly N/96 inches of the physical page, matching PaperWidthMM. To make
// content look bigger, change font-size/padding in the CSS itself, not
// this constant.
const contentDPI = 96.0

// contentZoom scales the whole report uniformly via CSS zoom, which
// reflows layout (unlike transform:scale), so wrapping and the height
// measurement stay accurate. The body's own declared width is divided by
// this factor so the zoomed-out physical footprint still exactly matches
// PaperWidthMM; only how big everything inside renders changes. 1.6 = 60%
// larger than the unzoomed baseline.
const contentZoom = 1.6

// Result describes a finished run.
type Result struct {
	PDFPath   string        `json:"pdfPath"`
	HTMLPath  string        `json:"htmlPath"`
	HeightMM  int           `json:"heightMm"`
	Sections  []SectionInfo `json:"sections"`
	Log       []string      `json:"log"`
	Duration  time.Duration `json:"duration"`
	Generated time.Time     `json:"generated"`
}

// SectionInfo reports what happened to each enabled section.
type SectionInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"` // ok | empty | error | disabled
	Error  string `json:"error,omitempty"`
}

// Renderer holds paths and the page template.
type Renderer struct {
	DataDir string
	OutDir  string
}

// New creates a renderer writing into dataDir/output.
func New(dataDir string) *Renderer {
	return &Renderer{DataDir: dataDir, OutDir: filepath.Join(dataDir, "output")}
}

// PDFPath is where the latest report lives.
func (r *Renderer) PDFPath() string { return filepath.Join(r.OutDir, "breaklist.pdf") }

// HTMLPath is the intermediate HTML.
func (r *Renderer) HTMLPath() string { return filepath.Join(r.OutDir, "breaklist.html") }

// RenderSections runs every enabled module concurrently and returns sections
// in configured order. Failures are logged and skipped so one dead API never
// blocks the morning report.
func (r *Renderer) RenderSections(ctx context.Context, cfg *config.Config, only map[string]bool, logf func(string, ...any)) ([]*modules.Section, []SectionInfo) {
	env := modules.NewEnv(cfg, r.DataDir, logf)
	type slot struct {
		sec  *modules.Section
		info SectionInfo
	}
	slots := make([]slot, len(cfg.Sections))
	var wg sync.WaitGroup
	for i, sc := range cfg.Sections {
		slots[i].info = SectionInfo{ID: sc.ID, Status: "disabled"}
		if only != nil {
			if !only[sc.ID] {
				continue
			}
		} else if !sc.Enabled {
			continue
		}
		mod, ok := modules.Get(sc.ID)
		if !ok {
			slots[i].info.Status = "error"
			slots[i].info.Error = "unknown module"
			continue
		}
		wg.Add(1)
		go func(i int, mod modules.Module, opt modules.Options) {
			defer wg.Done()
			mctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			start := time.Now()
			sec, err := safeRender(mctx, mod, env, opt)
			switch {
			case err != nil:
				slots[i].info.Status = "error"
				slots[i].info.Error = err.Error()
				logf("✗ %s: %v", mod.Info().ID, err)
			case sec == nil || sec.Empty:
				slots[i].info.Status = "empty"
				logf("· %s: nothing to show", mod.Info().ID)
			default:
				slots[i].sec = sec
				slots[i].info.Status = "ok"
				logf("✓ %s (%.1fs)", mod.Info().ID, time.Since(start).Seconds())
			}
		}(i, mod, modules.ApplyDefaults(mod.Info(), sc.Options))
	}
	wg.Wait()
	var secs []*modules.Section
	infos := make([]SectionInfo, 0, len(slots))
	for _, s := range slots {
		infos = append(infos, s.info)
		if s.sec != nil {
			secs = append(secs, s.sec)
		}
	}
	return secs, infos
}

func safeRender(ctx context.Context, mod modules.Module, env *modules.Env, opt modules.Options) (sec *modules.Section, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return mod.Render(ctx, env, opt)
}

// Page renders the sections into the full HTML document.
func (r *Renderer) Page(cfg *config.Config, secs []*modules.Section) (string, error) {
	var buf bytes.Buffer
	data := struct {
		Sections []*modules.Section
		WidthPx  int
		Zoom     float64
	}{secs, int(cfg.General.PaperWidthMM/25.4*contentDPI/contentZoom) - 4, contentZoom}
	if err := pageTpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Generate renders everything, writes HTML and PDF, and fits the page height.
func (r *Renderer) Generate(ctx context.Context, cfg *config.Config, only map[string]bool) (*Result, error) {
	start := time.Now()
	res := &Result{Generated: start}
	var mu sync.Mutex
	logf := func(format string, args ...any) {
		mu.Lock()
		res.Log = append(res.Log, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	secs, infos := r.RenderSections(ctx, cfg, only, logf)
	res.Sections = infos
	if len(secs) == 0 {
		return res, fmt.Errorf("no sections produced any content")
	}
	page, err := r.Page(cfg, secs)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(r.OutDir, 0o755); err != nil {
		return res, err
	}
	// wkhtmltopdf drops inline data: images, so spill them to files.
	page, err = r.externalizeImages(page)
	if err != nil {
		return res, err
	}
	if err := os.WriteFile(r.HTMLPath(), []byte(page), 0o644); err != nil {
		return res, err
	}
	res.HTMLPath = r.HTMLPath()

	est := 0
	for _, s := range secs {
		est += s.EstPx + 16 // divider + margins
	}
	heightMM := int(float64(est)*25.4/contentDPI*contentZoom) + 10
	pageWidthPx := int(cfg.General.PaperWidthMM/25.4*contentDPI/contentZoom) - 4
	if measured, err := r.measureHeightMM(ctx, cfg, r.HTMLPath(), pageWidthPx); err == nil && measured > 0 {
		logf("measured content height: %dmm (estimate %dmm)", measured, heightMM)
		heightMM = measured + 6
	} else if err != nil {
		logf("height measurement unavailable (%v), using estimate %dmm", err, heightMM)
	}
	if heightMM < 40 {
		heightMM = 40
	}
	for attempt := 0; attempt < 12; attempt++ {
		if err := r.runWkhtmltopdf(ctx, cfg, heightMM, r.HTMLPath(), r.PDFPath()); err != nil {
			return res, err
		}
		pages, err := pdfPageCount(r.PDFPath())
		if err != nil || pages <= 1 {
			break
		}
		logf("content overflowed to %d pages at %dmm, growing", pages, heightMM)
		heightMM = int(float64(heightMM) * 1.15)
	}
	res.PDFPath = r.PDFPath()
	res.HeightMM = heightMM
	res.Duration = time.Since(start)
	logf("done: %s (%dmm) in %.1fs", res.PDFPath, heightMM, res.Duration.Seconds())
	return res, nil
}

var dataImgRe = regexp.MustCompile(`data:image/(png|jpeg|gif);base64,([A-Za-z0-9+/=%&#;]+)`)

// externalizeImages writes every inline base64 image to OutDir/img and points
// the HTML at the file instead. Identical images share one file.
func (r *Renderer) externalizeImages(page string) (string, error) {
	imgDir := filepath.Join(r.OutDir, "img")
	if err := os.RemoveAll(imgDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		return "", err
	}
	seen := map[string]string{}
	var firstErr error
	out := dataImgRe.ReplaceAllStringFunc(page, func(m string) string {
		if firstErr != nil {
			return m
		}
		sub := dataImgRe.FindStringSubmatch(m)
		ext := sub[1]
		if ext == "jpeg" {
			ext = "jpg"
		}
		// html/template percent-encodes the base64 payload inside src attributes.
		payload, uerr := url.PathUnescape(html.UnescapeString(sub[2]))
		if uerr != nil {
			payload = html.UnescapeString(sub[2])
		}
		sum := sha1.Sum([]byte(payload))
		name := fmt.Sprintf("%x.%s", sum[:8], ext)
		if _, ok := seen[name]; !ok {
			raw, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				firstErr = err
				return m
			}
			if err := os.WriteFile(filepath.Join(imgDir, name), raw, 0o644); err != nil {
				firstErr = err
				return m
			}
			seen[name] = name
		}
		return "img/" + name
	})
	return out, firstErr
}

// measureHeightMM renders the given HTML file to a PNG with wkhtmltoimage
// (which sizes the image to its content) and converts the pixel height to
// millimetres. widthPx must match the CSS px width the HTML's own body
// declares itself (accounting for any zoom), so wkhtmltoimage lays out the
// content exactly as the final PDF render will.
func (r *Renderer) measureHeightMM(ctx context.Context, cfg *config.Config, htmlPath string, widthPx int) (int, error) {
	bin := r.findBinary(cfg, "wkhtmltoimage")
	if bin == "" {
		return 0, fmt.Errorf("wkhtmltoimage not found")
	}
	out := filepath.Join(r.OutDir, "measure.png")
	cmd := exec.CommandContext(ctx, bin, "--quiet", "--width", strconv.Itoa(widthPx), "--disable-smart-width", "--enable-local-file-access", "--encoding", "utf-8", htmlPath, out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(b)))
	}
	defer os.Remove(out)
	f, err := os.Open(out)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	// PNG IHDR: bytes 16..24 hold width and height as big-endian uint32.
	hdr := make([]byte, 24)
	if _, err := f.Read(hdr); err != nil {
		return 0, err
	}
	h := int(hdr[20])<<24 | int(hdr[21])<<16 | int(hdr[22])<<8 | int(hdr[23])
	// wkhtmltoimage output is at 96 dpi; wkhtmltopdf lays out slightly larger,
	// so scale conservatively.
	return int(float64(h)*25.4/contentDPI*1.06) + int(cfg.General.MarginBottomMM), nil
}

func (r *Renderer) runWkhtmltopdf(ctx context.Context, cfg *config.Config, heightMM int, htmlPath, pdfPath string) error {
	bin := r.findBinary(cfg, "wkhtmltopdf")
	if bin == "" {
		return fmt.Errorf("wkhtmltopdf not found: install it from https://wkhtmltopdf.org/downloads.html or set its path in General settings")
	}
	// Many receipt printers reserve a few mm on the left that the driver won't
	// print into. Reserving that as a real PDF page margin (rather than body
	// padding) keeps it printer-safe without shrinking the content area: the
	// page is widened by exactly the margin, so the content box stays the
	// full configured paper width.
	args := []string{"--quiet", "--encoding", "utf-8", "--margin-top", "1mm", "--margin-bottom", fmt.Sprintf("%.0fmm", cfg.General.MarginBottomMM),
		"--margin-left", fmt.Sprintf("%.0fmm", printerLeftMarginMM), "--margin-right", "0mm", "--page-height", fmt.Sprintf("%dmm", heightMM),
		"--page-width", fmt.Sprintf("%.0fmm", cfg.General.PaperWidthMM+printerLeftMarginMM),
		"--grayscale", "--disable-smart-shrinking", "--enable-local-file-access", htmlPath, pdfPath}
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		// wkhtmltopdf exits 1 for non-fatal warnings but still writes the file.
		if _, statErr := os.Stat(pdfPath); statErr == nil && strings.Contains(msg, "Warning") {
			return nil
		}
		return fmt.Errorf("wkhtmltopdf: %v: %s", err, msg)
	}
	return nil
}

// findBinary locates wkhtmltopdf / wkhtmltoimage.
func (r *Renderer) findBinary(cfg *config.Config, name string) string {
	if cfg.General.Wkhtmltopdf != "" {
		p := cfg.General.Wkhtmltopdf
		if name == "wkhtmltoimage" {
			p = strings.Replace(p, "wkhtmltopdf", "wkhtmltoimage", 1)
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	exe := name
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if p, err := exec.LookPath(exe); err == nil {
		return p
	}
	candidates := []string{filepath.Join(r.DataDir, exe)}
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			filepath.Join(os.Getenv("ProgramFiles"), "wkhtmltopdf", "bin", exe),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "wkhtmltopdf", "bin", exe),
			`C:\Program Files\wkhtmltopdf\bin\`+exe)
	} else {
		candidates = append(candidates, "/usr/local/bin/"+exe, "/usr/bin/"+exe, "/opt/homebrew/bin/"+exe)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

var countRe = regexp.MustCompile(`/Type\s*/Pages[^>]*?/Count\s+(\d+)`)

// pdfPageCount reads the page count from the PDF's page tree.
func pdfPageCount(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	best := 0
	for _, m := range countRe.FindAllSubmatch(data, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > best {
			best = n
		}
	}
	if best == 0 {
		// Fall back to counting page objects.
		best = bytes.Count(data, []byte("/Type /Page\n")) + bytes.Count(data, []byte("/Type/Page/"))
		if best == 0 {
			return 1, nil
		}
	}
	return best, nil
}

// Print runs the configured print command on the latest full report.
func (r *Renderer) Print(ctx context.Context, cfg *config.Config) (string, error) {
	if _, err := os.Stat(r.PDFPath()); err != nil {
		return "", fmt.Errorf("no report has been generated yet")
	}
	return r.PrintFile(ctx, cfg, r.PDFPath())
}

// PrintFile runs the configured print command against an arbitrary PDF, e.g.
// a one-off note from GenerateNote rather than the full report.
func (r *Renderer) PrintFile(ctx context.Context, cfg *config.Config, pdfPath string) (string, error) {
	cmdline := strings.TrimSpace(cfg.Print.Command)
	if cmdline == "" {
		return "", fmt.Errorf("no print command configured")
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return "", fmt.Errorf("file not found: %s", pdfPath)
	}
	cmdline = strings.ReplaceAll(cmdline, "{pdf}", pdfPath)
	var cmd *exec.Cmd
	if argv, ok := splitShellCommand(cmdline); ok {
		// Exec the program directly with a real argv array whenever the
		// command has no syntax only a shell understands (pipes, redirects,
		// chaining, env expansion). This matters a great deal on Windows:
		// routing a command like `powershell -Command "..."` through
		// `cmd /C "<string>"` re-quotes it, and cmd.exe's legacy /C parsing
		// does not round-trip nested double quotes correctly — PowerShell
		// ends up receiving its own outer quotes as literal text and treats
		// the whole thing as a string to print instead of a script to run,
		// silently no-op'ing the print while still exiting 0. A direct argv
		// exec sidesteps that layer of re-parsing entirely, on every OS.
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	} else if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/S", "/C", cmdline)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdline)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("print command failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// splitShellCommand tokenizes a command line the way a well-behaved shell
// would for a simple invocation: tokens split on whitespace, 'single quotes'
// taken completely literally, and "double quotes" allowing \" and \\
// escapes. It reports ok=false — meaning "don't use this, fall back to a
// real shell" — if the string contains a character that only means
// something to a shell (pipe, redirect, command separator, backtick, or
// unquoted $/% variable expansion), since those need actual shell semantics
// this tokenizer does not implement, or if its quoting is unbalanced.
func splitShellCommand(s string) (argv []string, ok bool) {
	var cur strings.Builder
	var tokens []string
	has := false
	inSingle, inDouble := false, false
	flush := func() {
		if has {
			tokens = append(tokens, cur.String())
			cur.Reset()
			has = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
			has = true
		case inDouble:
			if c == '"' {
				inDouble = false
			} else if c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
				cur.WriteByte(s[i+1])
				i++
			} else {
				cur.WriteByte(c)
			}
			has = true
		case c == '\'':
			inSingle, has = true, true
		case c == '"':
			inDouble, has = true, true
		case c == ' ' || c == '\t':
			flush()
		case strings.ContainsRune("|&<>;`$%\n\r", rune(c)):
			return nil, false
		default:
			cur.WriteByte(c)
			has = true
		}
	}
	if inSingle || inDouble {
		return nil, false
	}
	flush()
	if len(tokens) == 0 {
		return nil, false
	}
	return tokens, true
}

// NoteResult describes a finished "print this text" job. It is kept separate
// from Result/output/breaklist.pdf so an incoming note never collides with,
// or gets mistaken for, the scheduled morning report.
type NoteResult struct {
	PDFPath   string    `json:"pdfPath"`
	HeightMM  int       `json:"heightMm"`
	Generated time.Time `json:"generated"`
}

// GenerateNote renders a single block of text (not a report) to its own PDF
// under OutDir/notes, sized and height-fit the same way the full report is.
// Title may be blank. Old notes are pruned so the folder does not grow forever.
func (r *Renderer) GenerateNote(ctx context.Context, cfg *config.Config, title, text string) (*NoteResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("no text to print")
	}
	notesDir := filepath.Join(r.OutDir, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now()
	id := strings.NewReplacer(":", "", ".", "-").Replace(now.Format("20060102-150405.000"))
	htmlPath := filepath.Join(notesDir, id+".html")
	pdfPath := filepath.Join(notesDir, id+".pdf")

	widthPx := int(cfg.General.PaperWidthMM/25.4*contentDPI) - 4
	var buf bytes.Buffer
	if err := noteTpl.Execute(&buf, struct {
		WidthPx int
		Title   string
		Text    string
		Stamp   string
	}{widthPx, strings.TrimSpace(title), text, now.In(cfg.Location()).Format("Mon Jan 2, 3:04 PM")}); err != nil {
		return nil, err
	}
	if err := os.WriteFile(htmlPath, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}

	// Rough starting estimate (refined by measurement below, same as Generate).
	heightMM := 26 + (len([]rune(text))/24)*5
	if measured, err := r.measureHeightMM(ctx, cfg, htmlPath, widthPx); err == nil && measured > 0 {
		heightMM = measured + 6
	}
	if heightMM < 25 {
		heightMM = 25
	}
	for attempt := 0; attempt < 8; attempt++ {
		if err := r.runWkhtmltopdf(ctx, cfg, heightMM, htmlPath, pdfPath); err != nil {
			return nil, err
		}
		pages, err := pdfPageCount(pdfPath)
		if err != nil || pages <= 1 {
			break
		}
		heightMM = int(float64(heightMM) * 1.2)
	}
	r.pruneNotes(notesDir, 60)
	return &NoteResult{PDFPath: pdfPath, HeightMM: heightMM, Generated: now}, nil
}

// pruneNotes keeps only the newest `keep` files in the notes folder; names are
// timestamp-prefixed so lexical order is chronological order.
func (r *Renderer) pruneNotes(notesDir string, keep int) {
	entries, err := os.ReadDir(notesDir)
	if err != nil || len(entries) <= keep {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) <= keep {
		return
	}
	for _, n := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(notesDir, n))
	}
}

var noteTpl = template.Must(template.New("note").Parse(`<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<style>
body{margin:0;padding:5px 3px;width:{{.WidthPx}}px;font-family:Cambria,Cochin,Georgia,Times,'Times New Roman',serif;color:#000;background:#fff}
.hdr{font-size:13px;font-weight:bold;text-align:center;line-height:16px;margin-bottom:2px}
.divider{border-top:3px double #000;margin:6px 0}
.body{font-size:14px;line-height:19px;white-space:pre-wrap;overflow-wrap:break-word}
.stamp{font-size:7px;color:#555;text-align:center;margin-top:8px}
</style></head><body>
{{if .Title}}<div class="hdr">{{.Title}}</div><div class="divider"></div>{{end}}
<div class="body">{{.Text}}</div>
<div class="divider"></div>
<div class="stamp">{{.Stamp}}</div>
</body></html>`))

var pageTpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<style>
body{margin:0;padding:0 2px;width:{{.WidthPx}}px;zoom:{{.Zoom}};font-family:Cambria,Cochin,Georgia,Times,'Times New Roman',serif;color:#000;background:#fff}
img{image-rendering:pixelated}
.sec{page-break-inside:avoid}
.divider{border-top:3px double #000;margin:6px 0}
.hdr{font-size:14px;font-weight:bold;text-align:center;line-height:17px}
.h{font-size:8px;font-weight:bold;text-align:center;letter-spacing:.08em;text-transform:uppercase;margin:4px 0 3px;border-bottom:1px solid #000;padding-bottom:2px}
.t{font-size:12px;line-height:15px}
.s{font-size:8px;line-height:10px;color:#333}
.c{text-align:center}
.big{font-size:17px;font-weight:bold;line-height:21px}
.custom{white-space:pre-wrap}
.quote{font-size:12px;font-style:italic;text-align:center;margin:6px 4px;white-space:pre-wrap;line-height:15px}
ul.tasks{list-style:none;padding:0;margin:4px 0}
ul.tasks li{position:relative;padding-left:15px;font-size:12px;line-height:15px;margin-bottom:6px}
ul.tasks li:before{content:"";position:absolute;left:2px;top:3px;width:8px;height:8px;border:1.5px solid #000;border-radius:50%;box-sizing:border-box}
ul.news{list-style:circle;font-size:11px;line-height:14px;margin:4px 0 4px 1.2em;padding:0}
ul.news li{margin-bottom:5px}
.events{margin:3px 0}.ev{margin-bottom:5px}.src{color:#777}.ev-t{font-size:9px;font-weight:bold;line-height:11px}.ev-d{font-size:8px;color:#555;line-height:10px;margin-top:1px}
table.kv{width:100%;border-collapse:collapse;font-size:10px;line-height:13px}
table.kv td{padding:1px 0;vertical-align:top}table.kv td:first-child{font-weight:bold;width:40%}table.kv td:last-child{text-align:right}
table.wx-h{width:100%;border-collapse:collapse;table-layout:fixed;margin-bottom:2px}
table.wx-h td{text-align:center;vertical-align:top;padding:2px 1px}
.wx-h-time{font-size:8px;font-weight:bold;line-height:11px}.wx-h-temp{font-size:9px;font-weight:bold;line-height:12px}
.wx-sub{font-size:6px;color:#333;line-height:8px}
table.wx-d{width:100%;border-collapse:collapse;table-layout:fixed}
table.wx-d tr{border-bottom:1px dotted #aaa}table.wx-d tr:last-child{border-bottom:none}
table.wx-d td{vertical-align:middle;padding:3px 0;text-align:center}
.wx-d-day{width:26px}.wx-d-name{font-size:8px;font-weight:bold;line-height:10px}.wx-d-date{font-size:6px;color:#555;line-height:8px}
.wx-d-icon{width:24px}.wx-d-hilo{font-size:8px;font-weight:bold;line-height:11px;white-space:nowrap}.wx-d-hilo .lo{color:#555}
.img img{max-width:100%;margin:3px 0}
.imgwrap{overflow:hidden;width:100%;margin:3px 0;text-align:center;line-height:0}.imgwrap img{display:inline-block}
table.poke{width:100%;border-collapse:collapse}table.poke td{vertical-align:middle;padding:2px}table.poke td:first-child{width:44px}
.flip{-webkit-transform:rotate(180deg);transform:rotate(180deg);margin-top:4px;text-align:right}
table.kid2{width:100%;border-collapse:collapse;table-layout:fixed}table.kid2 td{text-align:center;vertical-align:top}
.kid-lbl{font-size:7px;text-transform:uppercase;letter-spacing:.05em}.kid-word{font-size:10px;font-weight:bold;line-height:13px}.kid-big{font-size:14px;font-weight:bold;line-height:18px}
table.routine{border-collapse:collapse;margin:2px 0}table.routine td{font-size:12px;line-height:18px;padding:0 4px 0 0}table.routine td.box{font-size:16px}
.wx-ico{display:inline-block;line-height:0}.wx-ico svg{display:block;margin:1px auto}
table.ranks{width:100%;border-collapse:collapse;font-size:9px;line-height:11px}table.ranks td{padding:0 2px 1px 0;vertical-align:top}table.ranks td.rk{font-weight:bold;width:14px;text-align:right}table.ranks td.rec{color:#555;text-align:right;white-space:nowrap}table.ranks td.tr{color:#555;width:16px;text-align:right}
.thin{border-top:1px solid #aaa;margin:4px 0}.game{margin:3px 0;font-size:10px;line-height:12px}
table.gtable{width:100%;border-collapse:collapse;font-size:8px;line-height:10px}table.gtable th{text-align:left;border-bottom:1px solid #000;font-weight:bold;padding:1px 2px}table.gtable td{padding:1px 2px;border-bottom:1px dotted #bbb;vertical-align:top}
.spark{font-family:'DejaVu Sans Mono','Segoe UI Symbol',monospace;font-size:11px;letter-spacing:-1px;line-height:14px;margin:2px 0}
</style></head><body>
{{range $i, $s := .Sections}}{{if $i}}<div class="divider"></div>{{end}}<div class="sec" data-id="{{$s.ID}}">{{$s.HTML}}</div>
{{end}}</body></html>`))
