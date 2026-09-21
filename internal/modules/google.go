package modules

import (
	"context"
	"sort"
	"strings"

	"tickr/internal/connectors"
)

// ── Google Tasks ─────────────────────────────────────────────────────────────

type googleTasksModule struct{}

var checklistTpl = Tpl("checklist", `<div class="h">{{.Title}}</div><ul class="tasks">{{range .Items}}<li>{{.}}</li>{{end}}</ul>{{if .Note}}<div class="s">{{.Note}}</div>{{end}}`)

func (googleTasksModule) Info() Info {
	return Info{
		ID: "google_tasks", Name: "Google Tasks", Category: CatCore, DefaultEnabled: false, Needs: []string{"google"},
		Description: "Open items from a Google Tasks list (the one behind Gmail and Calendar). Tick “Tasks” on the Google connector and re-link once.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Tasks"},
			{Key: "list", Label: "List name", Type: FieldText, Help: "Blank = your default list."},
			{Key: "max", Label: "Max items", Type: FieldNumber, Default: 10, Min: F64(1), Max: F64(50)},
			{Key: "dueFirst", Label: "Due-dated items first", Type: FieldBool, Default: true},
			{Key: "showDue", Label: "Show due dates", Type: FieldBool, Default: true},
			{Key: "onlyDueSoon", Label: "Only items due within (days)", Type: FieldNumber, Default: 0, Min: F64(0), Max: F64(60), Help: "0 = show everything."},
		},
	}
}

func (googleTasksModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	tasks, err := connectors.GoogleTasks(ctx, env.Cfg.Connectors.Google, strings.TrimSpace(opt.Str("list", "")), env.Loc)
	if err != nil {
		return nil, err
	}
	horizon := opt.Int("onlyDueSoon", 0)
	if horizon > 0 {
		limit := env.Now.AddDate(0, 0, horizon)
		var kept []connectors.GoogleTask
		for _, t := range tasks {
			if !t.Due.IsZero() && !t.Due.After(limit) {
				kept = append(kept, t)
			}
		}
		tasks = kept
	}
	if opt.Bool("dueFirst", true) {
		sort.SliceStable(tasks, func(i, j int) bool {
			a, b := tasks[i].Due, tasks[j].Due
			if a.IsZero() != b.IsZero() {
				return !a.IsZero()
			}
			return a.Before(b)
		})
	}
	var items []string
	for _, t := range tasks {
		s := t.Title
		if opt.Bool("showDue", true) && !t.Due.IsZero() {
			switch {
			case t.Due.Before(env.Now.Truncate(24 * 60 * 60 * 1e9)):
				s += " (overdue)"
			case sameDay(t.Due, env.Now):
				s += " (today)"
			default:
				s += " (" + t.Due.Format("Mon Jan 2") + ")"
			}
		}
		items = append(items, s)
		if len(items) == opt.Int("max", 10) {
			break
		}
	}
	if len(items) == 0 {
		return EmptySection("google_tasks"), nil
	}
	est := 20
	for _, it := range items {
		est += LinesPx(it, 30, 14) + 6
	}
	return Exec("google_tasks", checklistTpl, struct {
		Title, Note string
		Items       []string
	}{opt.Str("title", "Tasks"), "", items}, est)
}

// ── Google Keep ──────────────────────────────────────────────────────────────

type googleKeepModule struct{}

var keepTpl = Tpl("keep", `<div class="h">{{.Title}}</div>{{range .Notes}}{{if .Title}}<div class="t" style="font-weight:bold;margin-top:3px">{{.Title}}</div>{{end}}{{if .Text}}<div class="t custom">{{.Text}}</div>{{end}}{{if .Items}}<table class="routine">{{range .Items}}<tr><td class="box">{{if .Checked}}&#9745;{{else}}&#9744;{{end}}</td><td>{{.Text}}</td></tr>{{end}}</table>{{end}}{{end}}`)

func (googleKeepModule) Info() Info {
	return Info{
		ID: "google_keep", Name: "Google Keep note", Category: CatCore, DefaultEnabled: false, Needs: []string{"google"},
		Description: "Prints a Keep note or checklist by title. Note: Google only opens the Keep API to Workspace accounts; on a personal Gmail account this section reports a permission error, and Google Tasks is the workable alternative.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Notes"},
			{Key: "match", Label: "Note title contains", Type: FieldText, Help: "Blank prints the newest notes."},
			{Key: "max", Label: "Max notes", Type: FieldNumber, Default: 1, Min: F64(1), Max: F64(10)},
			{Key: "hideChecked", Label: "Hide checked items", Type: FieldBool, Default: true},
			{Key: "maxChars", Label: "Max characters per note", Type: FieldNumber, Default: 600, Min: F64(50), Max: F64(4000)},
		},
	}
}

func (googleKeepModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	notes, err := connectors.GoogleKeepNotes(ctx, env.Cfg.Connectors.Google)
	if err != nil {
		return nil, err
	}
	match := strings.ToLower(strings.TrimSpace(opt.Str("match", "")))
	type noteView struct {
		Title, Text string
		Items       []connectors.KeepItem
	}
	var out []noteView
	maxChars := opt.Int("maxChars", 600)
	est := 20
	for _, n := range notes {
		if match != "" && !strings.Contains(strings.ToLower(n.Title), match) {
			continue
		}
		v := noteView{Title: n.Title}
		if r := []rune(n.Text); len(r) > maxChars {
			v.Text = string(r[:maxChars-1]) + "…"
		} else {
			v.Text = n.Text
		}
		for _, it := range n.Items {
			if opt.Bool("hideChecked", true) && it.Checked {
				continue
			}
			v.Items = append(v.Items, it)
		}
		if v.Text == "" && len(v.Items) == 0 {
			continue
		}
		out = append(out, v)
		est += 14 + LinesPx(v.Text, 32, 14) + len(v.Items)*18
		if len(out) == opt.Int("max", 1) {
			break
		}
	}
	if len(out) == 0 {
		return EmptySection("google_keep"), nil
	}
	return Exec("google_keep", keepTpl, struct {
		Title string
		Notes []noteView
	}{opt.Str("title", "Notes"), out}, est)
}

func init() {
	Register(googleTasksModule{})
	Register(googleKeepModule{})
}
