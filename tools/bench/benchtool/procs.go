package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// proc is one service binary the stack runs.
type proc struct {
	Name  string
	Bin   string
	Args  []string
	Env   map[string]string
	HTTP  string // 127.0.0.1:port of /readyz; "" for the tls-agent
	Beat  string // the tls-agent's heartbeat file: ready once it exists
	Log   string
	cmd   *exec.Cmd
	done  chan struct{}
	err   error
	ready time.Duration
}

// deadProxy makes every outbound HTTP(S) request the services might try (an
// update check, a price, a license server) fail at once: the benchmark
// talks to nothing outside this machine. Go never proxies loopback
// addresses, so the services still reach each other and the fakes.
const deadProxy = "http://127.0.0.1:9"

// start runs p and waits until it is ready: /readyz answers 200, or the
// tls-agent's heartbeat file exists. The time from exec to ready is p.ready.
func (p *proc) start(timeout time.Duration) error {
	logf, err := os.OpenFile(p.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.TempDir(),
		"HTTP_PROXY=" + deadProxy, "HTTPS_PROXY=" + deadProxy, "http_proxy=" + deadProxy, "https_proxy=" + deadProxy}
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	if p.Beat != "" {
		_ = os.Remove(p.Beat)
	}
	p.cmd = exec.Command(p.Bin, p.Args...) //nolint:gosec // the variant's own service binaries
	p.cmd.Env, p.cmd.Stdout, p.cmd.Stderr = env, logf, logf
	p.done = make(chan struct{})
	begin := time.Now()
	if err := p.cmd.Start(); err != nil {
		_ = logf.Close()
		return fmt.Errorf("%s: %w", p.Name, err)
	}
	go func() {
		p.err = p.cmd.Wait()
		_ = logf.Close()
		close(p.done)
	}()
	hc := &http.Client{Timeout: time.Second}
	deadline := begin.Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return fmt.Errorf("%s exited before it was ready (%v): see %s", p.Name, p.err, p.Log)
		default:
		}
		if p.isReady(hc) {
			p.ready = time.Since(begin)
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("%s not ready after %s: see %s", p.Name, timeout, p.Log)
}

func (p *proc) isReady(hc *http.Client) bool {
	if p.Beat != "" {
		_, err := os.Stat(p.Beat)
		return err == nil
	}
	resp, err := hc.Get("http://" + p.HTTP + "/readyz") //nolint:noctx // a short local probe
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *proc) pid() int { return p.cmd.Process.Pid }

// stopped is how a service ended.
type stopped struct {
	ShutdownS float64 `json:"shutdown_s"`
	UserS     float64 `json:"user_s"`
	SysS      float64 `json:"sys_s"`
	MaxRSSMB  float64 `json:"max_rss_mb"`
	Exit      int     `json:"exit"`
}

// stop sends SIGTERM and waits (SIGKILL after 30 s): the time to exit and
// what the process used over its whole life (getrusage).
func (p *proc) stop() stopped {
	begin := time.Now()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
	s := stopped{ShutdownS: time.Since(begin).Seconds()}
	if ps := p.cmd.ProcessState; ps != nil {
		s.UserS, s.SysS, s.Exit = ps.UserTime().Seconds(), ps.SystemTime().Seconds(), ps.ExitCode()
		if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
			s.MaxRSSMB = maxRSSMB(ru.Maxrss)
		}
	}
	return s
}

// procUsage is one process's resident memory and CPU time so far.
type procUsage struct {
	RSSMB float64
	CPUS  float64
}

// sample reads RSS and the CPU time used so far for each pid, with one ps
// (macOS) or from /proc (Linux).
func sample(pids []int) (map[int]procUsage, error) {
	out := map[int]procUsage{}
	if runtime.GOOS == "linux" {
		tick := 100.0 // USER_HZ
		for _, pid := range pids {
			b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
			if err != nil {
				continue
			}
			s := string(b)
			f := strings.Fields(s[strings.LastIndex(s, ")")+2:])
			if len(f) < 22 {
				continue
			}
			ut, _ := strconv.ParseFloat(f[11], 64)
			st, _ := strconv.ParseFloat(f[12], 64)
			rss, _ := strconv.ParseFloat(f[21], 64)
			out[pid] = procUsage{RSSMB: rss * float64(os.Getpagesize()) / (1 << 20), CPUS: (ut + st) / tick}
		}
		return out, nil
	}
	ids := make([]string, len(pids))
	for i, p := range pids {
		ids[i] = strconv.Itoa(p)
	}
	b, err := exec.Command("ps", "-o", "pid=,rss=,time=", "-p", strings.Join(ids, ",")).Output() //nolint:gosec // pids we started
	if err != nil && len(b) == 0 {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		rss, _ := strconv.ParseFloat(f[1], 64)
		out[pid] = procUsage{RSSMB: rss / 1024, CPUS: psTime(f[2])}
	}
	return out, nil
}

// psTime parses ps's cumulative CPU time: [[hh:]mm:]ss[.cc].
func psTime(s string) float64 {
	var total float64
	for _, part := range strings.Split(s, ":") {
		v, _ := strconv.ParseFloat(part, 64)
		total = total*60 + v
	}
	return total
}

// sampler records the RSS of the stack's processes while a phase runs.
type sampler struct {
	mu   sync.Mutex
	rss  map[string][]float64
	stop chan struct{}
	wg   sync.WaitGroup
}

func startSampler(procs []*proc, every time.Duration) *sampler {
	s := &sampler{rss: map[string][]float64{}, stop: make(chan struct{})}
	s.wg.Go(func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			s.take(procs)
			select {
			case <-s.stop:
				return
			case <-t.C:
			}
		}
	})
	return s
}

func (s *sampler) take(procs []*proc) {
	pids := make([]int, len(procs))
	for i, p := range procs {
		pids[i] = p.pid()
	}
	u, err := sample(pids)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range procs {
		if x, ok := u[p.pid()]; ok {
			s.rss[p.Name] = append(s.rss[p.Name], x.RSSMB)
		}
	}
}

func (s *sampler) finish() map[string][]float64 {
	close(s.stop)
	s.wg.Wait()
	return s.rss
}

// cpuNow is each process's CPU seconds so far, by name.
func cpuNow(procs []*proc) map[string]float64 {
	pids := make([]int, len(procs))
	for i, p := range procs {
		pids[i] = p.pid()
	}
	u, _ := sample(pids)
	out := map[string]float64{}
	for _, p := range procs {
		out[p.Name] = u[p.pid()].CPUS
	}
	return out
}

// freePorts asks the kernel for n distinct unused loopback ports.
func freePorts(n int) ([]string, error) {
	// Hold them all open until the end, so no two are the same.
	var ls []net.Listener
	defer func() {
		for _, l := range ls {
			_ = l.Close()
		}
	}()
	var out []string
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		ls = append(ls, l)
		out = append(out, l.Addr().String())
	}
	return out, nil
}

// waitFor polls cond until it is true or ctx ends.
func waitFor(ctx context.Context, every time.Duration, cond func() (bool, error)) error {
	for {
		ok, err := cond()
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.New("timed out")
		case <-time.After(every):
		}
	}
}
