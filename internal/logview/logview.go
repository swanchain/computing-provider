// Package logview reads the provider's own log files back for the dashboard,
// filtered by level, time range and text.
//
// internal/logging writes one rotating file per level group — info.log
// (trace, debug, info), warn.log and error.log (error, fatal, panic) — with
// lumberjack, which renames a full file to <name>-<UTC timestamp>.log and
// optionally gzips it. Lines are logrus text format:
//
//	time="2026-10-17 02:50:39.348" level=info msg="..." func=f file="x.go:12"
//
// Results are newest first. The current file of each group is read backwards
// from its end and the read stops at the start of the range, so the common
// question — what went wrong in the last hour — costs a read of the last hour,
// not of a 100 MB file.
package logview

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Level is a logrus level name as it appears in the file.
type Level string

const (
	LevelError   Level = "error"
	LevelWarning Level = "warning"
	LevelInfo    Level = "info"
	LevelDebug   Level = "debug"
)

// groups maps each file to the levels internal/logging routes into it.
var groups = []struct {
	file   string
	levels []string
}{
	{"error.log", []string{"error", "fatal", "panic"}},
	{"warn.log", []string{"warning"}},
	{"info.log", []string{"info", "debug", "trace"}},
}

// scanBudget is defaultScanBytes, replaceable in tests.
var scanBudget int64 = defaultScanBytes

// timeFormat is the SDK logger's TimestampFormat, written in local time.
const timeFormat = "2006-01-02 15:04:05.000"

// backupTimeFormat is lumberjack's, always UTC since LocalTime is not set.
const backupTimeFormat = "2006-01-02T15-04-05.000"

const (
	DefaultLimit = 200
	MaxLimit     = 2000
	// defaultScanBytes bounds the work one query can do. A narrow filter over a
	// week of rotated files would otherwise decompress gigabytes per request.
	defaultScanBytes = 512 << 20
	// maxLineBytes drops pathological lines rather than buffering them.
	maxLineBytes = 1 << 20
)

// Query selects entries.
type Query struct {
	Levels []Level // empty means all
	Since  time.Time
	Until  time.Time // zero means now
	// Contains is matched case-insensitively against the message and the
	// source location.
	Contains string
	Limit    int
}

// Entry is one log line.
type Entry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	// Func and File say where the line was logged, when the logger recorded it.
	Func string `json:"func,omitempty"`
	File string `json:"file,omitempty"`
}

// Result is a page of entries, newest first.
type Result struct {
	Entries []Entry `json:"entries"`
	// Truncated is true when more entries matched than Limit, or the scan
	// budget ran out before the start of the range was reached. Either way
	// the range is not complete and a narrower one should be asked for.
	Truncated bool `json:"truncated"`
	// Partial names why the scan stopped early, when it did.
	Partial string `json:"partial,omitempty"`
	// Files are the files actually read, for the operator who wants to go to
	// the source.
	Files []string `json:"files"`
}

// Read runs q against the log files in dir.
func Read(dir string, q Query) (Result, error) {
	if dir == "" {
		return Result{}, errors.New("log directory is not configured")
	}
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit > MaxLimit {
		q.Limit = MaxLimit
	}
	if q.Until.IsZero() {
		q.Until = time.Now()
	}
	if !q.Since.IsZero() && q.Until.Before(q.Since) {
		return Result{}, errors.New("until is before since")
	}
	want := wantedLevels(q.Levels)
	needle := strings.ToLower(strings.TrimSpace(q.Contains))

	// One past the limit per file, so the merge can tell there were more.
	s := &scan{q: q, limit: q.Limit + 1, want: want, needle: needle, budget: scanBudget}
	res := Result{Files: []string{}}
	var all []Entry
	for _, g := range groups {
		if !groupWanted(g.levels, want) {
			continue
		}
		entries, files, err := s.group(dir, g.file)
		if err != nil {
			return Result{}, err
		}
		all = append(all, entries...)
		res.Files = append(res.Files, files...)
	}

	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.After(all[j].Time) })
	if len(all) > q.Limit {
		all = all[:q.Limit]
		res.Truncated = true
	}
	if s.exhausted {
		res.Truncated = true
		res.Partial = "scan limit reached before the start of the range; narrow the range or the filters"
	}
	if all == nil {
		all = []Entry{}
	}
	res.Entries = all
	return res, nil
}

func wantedLevels(levels []Level) map[string]bool {
	if len(levels) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, l := range levels {
		switch l {
		case LevelError:
			want["error"], want["fatal"], want["panic"] = true, true, true
		case LevelWarning, "warn":
			want["warning"] = true
		case LevelInfo:
			want["info"] = true
		case LevelDebug:
			want["debug"], want["trace"] = true, true
		}
	}
	return want
}

func groupWanted(levels []string, want map[string]bool) bool {
	if want == nil {
		return true
	}
	for _, l := range levels {
		if want[l] {
			return true
		}
	}
	return false
}

// ParseLevels reads a comma-separated list, rejecting unknown names so a typo
// is an error rather than an empty result.
func ParseLevels(s string) ([]Level, error) {
	var out []Level
	for _, part := range strings.Split(s, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		switch part {
		case "":
			continue
		case "error", "warning", "info", "debug":
			out = append(out, Level(part))
		case "warn":
			out = append(out, LevelWarning)
		default:
			return nil, fmt.Errorf("unknown level %q (want error, warning, info or debug)", part)
		}
	}
	return out, nil
}

type scan struct {
	q         Query
	limit     int
	want      map[string]bool
	needle    string
	budget    int64
	exhausted bool
}

// group reads one level group: the current file backwards, then each rotated
// backup that can hold lines in the range, newest first, until Limit matches
// are found or the range is passed.
func (s *scan) group(dir, name string) ([]Entry, []string, error) {
	var out []Entry
	var files []string

	current := filepath.Join(dir, name)
	reachedStart, err := s.readBackwards(current, &out)
	switch {
	case err == nil:
		files = append(files, current)
	case errors.Is(err, os.ErrNotExist):
		// Nothing logged at this level yet.
	default:
		return nil, nil, err
	}
	if reachedStart || len(out) >= s.limit || s.exhausted {
		return out, files, nil
	}

	for _, b := range backups(dir, name) {
		// A backup holds lines from before its rotation time only.
		if !s.q.Since.IsZero() && b.rotated.Before(s.q.Since) {
			break
		}
		if s.exhausted {
			break
		}
		got, err := s.readForward(b.path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", b.path, err)
		}
		files = append(files, b.path)
		out = append(out, got...)
		if len(out) >= s.limit {
			break
		}
	}
	return out, files, nil
}

type backup struct {
	path    string
	rotated time.Time
}

// backups lists lumberjack's rotated copies of name, newest first.
func backups(dir, name string) []backup {
	ext := filepath.Ext(name)
	prefix := strings.TrimSuffix(name, ext) + "-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []backup
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) {
			continue
		}
		stamp := strings.TrimPrefix(n, prefix)
		stamp = strings.TrimSuffix(stamp, ".gz")
		if !strings.HasSuffix(stamp, ext) {
			continue
		}
		t, err := time.Parse(backupTimeFormat, strings.TrimSuffix(stamp, ext))
		if err != nil {
			continue
		}
		out = append(out, backup{path: filepath.Join(dir, n), rotated: t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rotated.After(out[j].rotated) })
	return out
}

// accept parses a line and keeps it if it matches. It reports whether the
// line is older than the range, which tells a backwards read it can stop.
func (s *scan) accept(line []byte, out *[]Entry) (older bool) {
	e, ok := ParseLine(line)
	if !ok {
		return false
	}
	if !s.q.Since.IsZero() && e.Time.Before(s.q.Since) {
		return true
	}
	if e.Time.After(s.q.Until) {
		return false
	}
	if s.want != nil && !s.want[e.Level] {
		return false
	}
	if s.needle != "" &&
		!strings.Contains(strings.ToLower(e.Message), s.needle) &&
		!strings.Contains(strings.ToLower(e.Func), s.needle) &&
		!strings.Contains(strings.ToLower(e.File), s.needle) {
		return false
	}
	*out = append(*out, e)
	return false
}

// readBackwards reads path from the end, newest line first. It reports
// whether it reached a line older than the range.
func (s *scan) readBackwards(path string, out *[]Entry) (reachedStart bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}

	const chunk = 64 << 10
	pos := info.Size()
	var carry []byte // the start of a line whose beginning is in an earlier chunk
	buf := make([]byte, chunk)
	start := len(*out)
	for pos > 0 {
		if len(*out)-start >= s.limit {
			return false, nil
		}
		if s.budget <= 0 {
			s.exhausted = true
			return false, nil
		}
		n := int64(chunk)
		if pos < n {
			n = pos
		}
		pos -= n
		if _, err := f.ReadAt(buf[:n], pos); err != nil && err != io.EOF {
			return false, err
		}
		s.budget -= n
		data := append(append([]byte{}, buf[:n]...), carry...)
		// Every complete line in data except the first, which may continue
		// into the previous chunk unless this chunk starts the file.
		for {
			i := bytes.LastIndexByte(data, '\n')
			if i < 0 {
				break
			}
			line := data[i+1:]
			data = data[:i]
			if len(line) == 0 {
				continue
			}
			if s.accept(line, out) {
				return true, nil
			}
			if len(*out)-start >= s.limit {
				return false, nil
			}
		}
		carry = data
		if len(carry) > maxLineBytes {
			carry = nil
		}
	}
	if len(carry) > 0 && s.accept(carry, out) {
		return true, nil
	}
	return false, nil
}

// readForward scans a rotated backup start to end, which a gzip stream
// requires, keeping only the newest Limit matches.
func (s *scan) readForward(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLineBytes)
	var kept []Entry
	for sc.Scan() {
		line := sc.Bytes()
		s.budget -= int64(len(line) + 1)
		if s.budget <= 0 {
			s.exhausted = true
			break
		}
		var one []Entry
		s.accept(line, &one)
		kept = append(kept, one...)
		if len(kept) > s.limit*2 {
			kept = append([]Entry{}, kept[len(kept)-s.limit:]...)
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return nil, err
	}
	// Newest first, as the current file is.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	if len(kept) > s.limit {
		kept = kept[:s.limit]
	}
	return kept, nil
}

// ParseLine reads one logrus text-format line. Lines without a parseable time
// and level are not entries and are reported as such.
func ParseLine(line []byte) (Entry, bool) {
	var e Entry
	var haveTime bool
	rest := string(bytes.TrimRight(line, "\r"))
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " ")
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			break
		}
		key := rest[:eq]
		rest = rest[eq+1:]
		var val string
		if strings.HasPrefix(rest, `"`) {
			end := closingQuote(rest)
			if end < 0 {
				return Entry{}, false
			}
			v, err := strconv.Unquote(rest[:end+1])
			if err != nil {
				v = rest[1:end]
			}
			val, rest = v, rest[end+1:]
		} else {
			sp := strings.IndexByte(rest, ' ')
			if sp < 0 {
				sp = len(rest)
			}
			val, rest = rest[:sp], rest[sp:]
		}
		switch key {
		case "time":
			t, err := time.ParseInLocation(timeFormat, val, time.Local)
			if err != nil {
				return Entry{}, false
			}
			e.Time, haveTime = t, true
		case "level":
			e.Level = val
		case "msg":
			e.Message = val
		case "func":
			e.Func = val
		case "file":
			e.File = val
		}
	}
	if !haveTime || e.Level == "" {
		return Entry{}, false
	}
	return e, true
}

// closingQuote finds the quote ending a Go-quoted string that starts at s[0].
func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}
