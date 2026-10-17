package logview

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 10, 17, 2, 0, 0, 0, time.Local)

func line(at time.Time, level, msg string) string {
	return fmt.Sprintf("time=%q level=%s msg=%q func=handle file=\"x.go:1\"\n", at.Format(timeFormat), level, msg)
}

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeGz(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(strings.Join(lines, ""))); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func messages(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Message
	}
	return out
}

func fixture(t *testing.T) string {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "info.log"),
		line(base.Add(1*time.Minute), "info", "i1"),
		line(base.Add(3*time.Minute), "debug", "d3"),
		line(base.Add(5*time.Minute), "info", "i5"),
	)
	write(t, filepath.Join(dir, "warn.log"),
		line(base.Add(2*time.Minute), "warning", "w2"),
		line(base.Add(6*time.Minute), "warning", "w6"),
	)
	write(t, filepath.Join(dir, "error.log"),
		line(base.Add(4*time.Minute), "error", `e4 "quoted" Post "http://x"`),
	)
	return dir
}

// The three files are one log; read back they are merged and newest first.
func TestReadMergesLevelsNewestFirst(t *testing.T) {
	res, err := Read(fixture(t), Query{Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(messages(res.Entries), ",")
	want := `w6,i5,e4 "quoted" Post "http://x",d3,w2,i1`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if res.Truncated {
		t.Error("truncated with everything returned")
	}
}

func TestReadFiltersByLevel(t *testing.T) {
	dir := fixture(t)
	for _, tc := range []struct {
		levels []Level
		want   string
	}{
		{[]Level{LevelError}, `e4 "quoted" Post "http://x"`},
		{[]Level{LevelError, LevelWarning}, `w6,e4 "quoted" Post "http://x",w2`},
		{[]Level{LevelInfo}, "i5,i1"},
		{[]Level{LevelDebug}, "d3"},
	} {
		res, err := Read(dir, Query{Levels: tc.levels, Until: base.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(messages(res.Entries), ","); got != tc.want {
			t.Errorf("levels %v: got %s, want %s", tc.levels, got, tc.want)
		}
	}
}

// Asking for errors only must not read info.log, the one that is large.
func TestReadOpensOnlyTheFilesTheLevelsNeed(t *testing.T) {
	res, err := Read(fixture(t), Query{Levels: []Level{LevelError}, Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || filepath.Base(res.Files[0]) != "error.log" {
		t.Fatalf("files = %v, want error.log only", res.Files)
	}
}

func TestReadFiltersByRange(t *testing.T) {
	res, err := Read(fixture(t), Query{Since: base.Add(2 * time.Minute), Until: base.Add(4 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(messages(res.Entries), ","); got != `e4 "quoted" Post "http://x",d3,w2` {
		t.Fatalf("got %s", got)
	}
}

func TestReadFiltersByText(t *testing.T) {
	res, err := Read(fixture(t), Query{Contains: "QUOTED", Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Level != "error" {
		t.Fatalf("got %+v", res.Entries)
	}
	if res.Entries[0].File != "x.go:1" || res.Entries[0].Func != "handle" {
		t.Errorf("source not parsed: %+v", res.Entries[0])
	}
}

// The newest Limit entries are returned, and the reader says there were more.
func TestReadLimitsAndReportsTruncation(t *testing.T) {
	res, err := Read(fixture(t), Query{Limit: 2, Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(messages(res.Entries), ","); got != "w6,i5" || !res.Truncated {
		t.Fatalf("got %s truncated=%v, want w6,i5 truncated", got, res.Truncated)
	}
}

// A file far larger than one read chunk, with lines straddling chunk
// boundaries, must come back whole and in order.
func TestReadBackwardsAcrossChunks(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	pad := strings.Repeat("x", 700)
	for i := 0; i < 1000; i++ {
		lines = append(lines, line(base.Add(time.Duration(i)*time.Second), "warning", fmt.Sprintf("m%04d %s", i, pad)))
	}
	// The last line has no trailing newline, as a file being written can.
	lines[len(lines)-1] = strings.TrimSuffix(lines[len(lines)-1], "\n")
	write(t, filepath.Join(dir, "warn.log"), lines...)

	res, err := Read(dir, Query{Limit: MaxLimit, Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1000 {
		t.Fatalf("got %d entries, want 1000", len(res.Entries))
	}
	for i, e := range res.Entries {
		want := fmt.Sprintf("m%04d", 999-i)
		if !strings.HasPrefix(e.Message, want) {
			t.Fatalf("entry %d = %.10s, want %s", i, e.Message, want)
		}
	}
}

// A backwards read stops at the start of the range instead of reading the
// whole file.
func TestReadStopsAtTheStartOfTheRange(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 5000; i++ {
		lines = append(lines, line(base.Add(time.Duration(i)*time.Second), "info", strings.Repeat("y", 200)))
	}
	write(t, filepath.Join(dir, "info.log"), lines...)

	old := scanBudget
	scanBudget = 200 << 10 // far below the file, well above the last 10 s
	defer func() { scanBudget = old }()

	res, err := Read(dir, Query{Since: base.Add(4990 * time.Second), Until: base.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 10 || res.Truncated {
		t.Fatalf("got %d entries truncated=%v, want the last 10 without hitting the budget", len(res.Entries), res.Truncated)
	}
}

func TestReadReportsAnExhaustedBudget(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 5000; i++ {
		lines = append(lines, line(base.Add(time.Duration(i)*time.Second), "info", strings.Repeat("y", 200)))
	}
	write(t, filepath.Join(dir, "info.log"), lines...)

	old := scanBudget
	scanBudget = 100 << 10
	defer func() { scanBudget = old }()

	res, err := Read(dir, Query{Contains: "never matches", Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Partial == "" {
		t.Fatalf("got truncated=%v partial=%q, want the early stop reported", res.Truncated, res.Partial)
	}
}

// A range older than the current file is answered from lumberjack's rotated
// backups, gzipped or not; backups rotated before the range are not opened.
func TestReadReachesIntoRotatedBackups(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "error.log"), line(base.Add(3*time.Hour), "error", "current"))
	rot1 := base.Add(2 * time.Hour).UTC().Format(backupTimeFormat)
	rot2 := base.Add(1 * time.Hour).UTC().Format(backupTimeFormat)
	rot0 := base.Add(-1 * time.Hour).UTC().Format(backupTimeFormat)
	writeGz(t, filepath.Join(dir, "error-"+rot1+".log.gz"),
		line(base.Add(90*time.Minute), "error", "backup1-a"),
		line(base.Add(100*time.Minute), "error", "backup1-b"),
	)
	write(t, filepath.Join(dir, "error-"+rot2+".log"), line(base.Add(30*time.Minute), "error", "backup2"))
	write(t, filepath.Join(dir, "error-"+rot0+".log"), line(base.Add(-2*time.Hour), "error", "too-old"))

	res, err := Read(dir, Query{Since: base, Until: base.Add(4 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(messages(res.Entries), ","); got != "current,backup1-b,backup1-a,backup2" {
		t.Fatalf("got %s", got)
	}
	for _, f := range res.Files {
		if strings.Contains(f, rot0) {
			t.Errorf("opened %s, rotated before the range", f)
		}
	}
}

func TestReadWithNoLogsYet(t *testing.T) {
	res, err := Read(t.TempDir(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 || res.Entries == nil {
		t.Fatalf("got %#v, want an empty, non-nil list", res.Entries)
	}
}

func TestReadRejectsAnInvertedRange(t *testing.T) {
	if _, err := Read(t.TempDir(), Query{Since: base, Until: base.Add(-time.Minute)}); err == nil {
		t.Fatal("want an error")
	}
}

func TestParseLevels(t *testing.T) {
	got, err := ParseLevels("error, warn,INFO")
	if err != nil || len(got) != 3 || got[1] != LevelWarning {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := ParseLevels("eror"); err == nil {
		t.Fatal("a typo must be an error, not an empty result")
	}
}

func TestParseLineSkipsNonEntries(t *testing.T) {
	for _, s := range []string{
		"",
		"[GIN] 2026/10/17 - 03:40:32 | 200 | GET /api",
		`level=info msg="no time"`,
		`time="2026-10-17 02:50:39.348" msg="no level"`,
		`time="2026-10-17 02:50:39.348" level=info msg="unterminated`,
	} {
		if _, ok := ParseLine([]byte(s)); ok {
			t.Errorf("parsed %q as an entry", s)
		}
	}
	e, ok := ParseLine([]byte(`time="2026-10-17 02:50:39.348" level=info msg="a\nb \"c\"" func=f`))
	if !ok || e.Message != "a\nb \"c\"" || e.Func != "f" {
		t.Fatalf("got %+v ok=%v", e, ok)
	}
}

// Exactly Limit entries in one file and more behind them is still a
// truncated answer.
func TestReadReportsTruncationWhenOneFileFillsTheLimit(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, line(base.Add(time.Duration(i)*time.Second), "error", fmt.Sprintf("e%d", i)))
	}
	write(t, filepath.Join(dir, "error.log"), lines...)
	res, err := Read(dir, Query{Levels: []Level{LevelError}, Limit: 5, Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 5 || !res.Truncated {
		t.Fatalf("got %d truncated=%v, want 5 truncated", len(res.Entries), res.Truncated)
	}
	res, err = Read(dir, Query{Levels: []Level{LevelError}, Limit: 10, Until: base.Add(time.Hour)})
	if err != nil || len(res.Entries) != 10 || res.Truncated {
		t.Fatalf("got %d truncated=%v err=%v, want all 10, not truncated", len(res.Entries), res.Truncated, err)
	}
}
