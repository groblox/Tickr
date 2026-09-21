package render

import (
	"reflect"
	"testing"
)

func TestSplitShellCommand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
		ok   bool
	}{
		{
			name: "powershell with nested single-quoted args inside a double-quoted script",
			in:   `powershell -NoProfile -Command "Copy-Item -Force '{pdf}' 'C:/out.pdf'"`,
			want: []string{"powershell", "-NoProfile", "-Command", "Copy-Item -Force '{pdf}' 'C:/out.pdf'"},
			ok:   true,
		},
		{
			name: "the shipped Windows default print command",
			in:   `powershell -NoProfile -Command "Start-Process -FilePath 'C:/out.pdf' -Verb Print -WindowStyle Hidden"`,
			want: []string{"powershell", "-NoProfile", "-Command", "Start-Process -FilePath 'C:/out.pdf' -Verb Print -WindowStyle Hidden"},
			ok:   true,
		},
		{
			name: "simple lp command",
			in:   `lp "/tmp/out.pdf"`,
			want: []string{"lp", "/tmp/out.pdf"},
			ok:   true,
		},
		{
			name: "lp with a named printer",
			in:   `lp -d PRINTER_NAME "/tmp/out.pdf"`,
			want: []string{"lp", "-d", "PRINTER_NAME", "/tmp/out.pdf"},
			ok:   true,
		},
		{
			name: "pipe requires a real shell",
			in:   `echo hi | findstr hi`,
			ok:   false,
		},
		{
			name: "redirect requires a real shell",
			in:   `echo hi > out.txt`,
			ok:   false,
		},
		{
			name: "chaining requires a real shell",
			in:   `lp a.pdf && echo done`,
			ok:   false,
		},
		{
			name: "unquoted env var requires a real shell",
			in:   `echo %USERNAME%`,
			ok:   false,
		},
		{
			name: "quoted $ is passed through untouched",
			in:   `powershell -Command "Write-Output $env:USERNAME"`,
			want: []string{"powershell", "-Command", "Write-Output $env:USERNAME"},
			ok:   true,
		},
		{
			name: "unbalanced quote",
			in:   `lp "unterminated`,
			ok:   false,
		},
		{
			name: "blank command",
			in:   ``,
			ok:   false,
		},
		{
			name: "escaped double quote inside a double-quoted token",
			in:   `echo "say \"hi\""`,
			want: []string{"echo", `say "hi"`},
			ok:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := splitShellCommand(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (argv=%#v)", ok, tc.ok, got)
			}
			if ok && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv = %#v, want %#v", got, tc.want)
			}
		})
	}
}
