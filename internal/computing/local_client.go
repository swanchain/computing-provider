package computing

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Who sent a local-gateway request.
//
// Every local client connects from 127.0.0.1, so the address says nothing. Two
// things do: the User-Agent the client chose to send, and the process that owns
// the other end of the connection, which Linux reports through /proc. The
// second is the one a client cannot leave out — a batch job that held a
// subscription-backed model for an hour and exhausted its quota had sent no
// identifying header at all.
//
// Both are best effort. Off Linux, or for a process owned by another user whose
// file descriptors cannot be read, the process part narrows to the owning user
// or is left out; nothing here can fail a request.

// maxClientBytes caps the stored description; it is written once per request.
const maxClientBytes = 200

// describeLocalClient returns a short description of the caller, such as
// "python3 (pid 41233, ccao) · python-requests/2.32.3", or "" when nothing
// is known.
func describeLocalClient(r *http.Request) string {
	var parts []string
	if proc := localPeerProcess(r.RemoteAddr); proc != "" {
		parts = append(parts, proc)
	}
	if ua := strings.TrimSpace(r.UserAgent()); ua != "" {
		parts = append(parts, sanitizeClientText(ua))
	}
	desc := strings.Join(parts, " · ")
	if len(desc) > maxClientBytes {
		desc = desc[:maxClientBytes]
	}
	return desc
}

// sanitizeClientText drops control characters: a User-Agent is chosen by the
// client and ends up in the dashboard and in logs.
func sanitizeClientText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// localPeerProcess names the process on the client side of a loopback
// connection, given the request's remote address.
func localPeerProcess(remoteAddr string) string {
	_, portStr, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return ""
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return ""
	}
	inode, uid, ok := socketForLocalPort(port)
	if !ok {
		return ""
	}
	// A client that keeps its connection alive sends every request over the
	// same socket, so the /proc scan is paid once per connection.
	if desc, ok := peerCache.get(inode); ok {
		return desc
	}
	desc := describePeer(inode, uid)
	peerCache.put(inode, desc)
	return desc
}

func describePeer(inode string, uid int) string {
	owner := userName(uid)
	pid := pidForSocket(inode)
	if pid == 0 {
		if owner == "" {
			return ""
		}
		return "user " + owner
	}
	name := processName(pid)
	if name == "" {
		name = "pid"
	}
	if owner != "" {
		return fmt.Sprintf("%s (pid %d, %s)", name, pid, owner)
	}
	return fmt.Sprintf("%s (pid %d)", name, pid)
}

// socketForLocalPort finds the socket whose local port is the client's port,
// in /proc/net/tcp and tcp6. It returns the socket inode and the owning uid.
func socketForLocalPort(port int) (inode string, uid int, ok bool) {
	want := fmt.Sprintf(":%04X", port)
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if inode, uid, ok = scanSocketTable(table, want); ok {
			return inode, uid, true
		}
	}
	return "", 0, false
}

// scanSocketTable looks for a row whose local address ends in wantPort. Rows
// are: sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode.
func scanSocketTable(path, wantPort string) (string, int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 || !strings.HasSuffix(fields[1], wantPort) {
			continue
		}
		uid, err := strconv.Atoi(fields[7])
		if err != nil || fields[9] == "0" {
			continue
		}
		return fields[9], uid, true
	}
	return "", 0, false
}

// pidForSocket finds the process holding a socket inode by reading /proc/*/fd.
// Processes whose descriptors cannot be read are skipped.
func pidForSocket(inode string) int {
	target := "socket:[" + inode + "]"
	dirs, err := filepath.Glob("/proc/[0-9]*/fd")
	if err != nil {
		return 0
	}
	for _, dir := range dirs {
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if link, err := os.Readlink(filepath.Join(dir, fd.Name())); err == nil && link == target {
				pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(dir)))
				return pid
			}
		}
	}
	return 0
}

// processName is the executable's short name. Deliberately not the command
// line: arguments can carry API keys and file paths that do not belong in a
// request history.
func processName(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return sanitizeClientText(strings.TrimSpace(string(data)))
}

func userName(uid int) string {
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return strconv.Itoa(uid)
	}
	return u.Username
}

// peerCache maps a socket inode to its description. Inodes are not reused
// while the socket lives, and a stale entry for a closed one is harmless: the
// next socket gets a fresh inode. Cleared wholesale when full rather than
// tracking recency, since a burst of one-shot connections is the only way to
// fill it.
var peerCache = &socketCache{entries: map[string]string{}}

const peerCacheSize = 256

type socketCache struct {
	mu      sync.Mutex
	entries map[string]string
}

func (c *socketCache) get(inode string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.entries[inode]
	return d, ok
}

func (c *socketCache) put(inode, desc string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= peerCacheSize {
		c.entries = map[string]string{}
	}
	c.entries[inode] = desc
}
