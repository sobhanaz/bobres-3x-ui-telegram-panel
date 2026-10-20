package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// kubectl plays kubectl (or k3s) with recorded output.
type kubectl struct {
	t       *testing.T
	have    map[string]bool // programs on the host
	nodes   []byte
	pods    []byte
	failing bool
	calls   [][]string
}

func (k *kubectl) look(name string) (string, error) {
	if k.have[name] {
		return "/usr/local/bin/" + name, nil
	}
	return "", exec.ErrNotFound
}

func (k *kubectl) run(_ context.Context, name string, args ...string) ([]byte, error) {
	k.calls = append(k.calls, append([]string{name}, args...))
	if k.failing {
		return nil, errors.New("exit status 1")
	}
	line := strings.Join(args, " ")
	switch {
	case strings.Contains(line, "get nodes"):
		return k.nodes, nil
	case strings.Contains(line, "get pods"):
		return k.pods, nil
	}
	k.t.Fatalf("unexpected call: %s %s", name, line)
	return nil, nil
}

func TestKubernetesNodesAndPods(t *testing.T) {
	k := &kubectl{t: t, have: map[string]bool{"kubectl": true}, nodes: fixture(t, "kube/nodes.json"), pods: fixture(t, "kube/pods.json")}
	got := collectKube(context.Background(), KubeProbe{Look: k.look, Run: k.run})
	if !got.Detected || got.Via != "kubectl" || got.Reason != "" || len(got.Nodes) != 2 || len(got.Pods) != 5 {
		t.Fatalf("%+v", got)
	}
	if n := got.Nodes[0]; n.Name != "control-1" || n.Ready || n.Roles != "control-plane,master" || n.Version != "v1.30.4+k3s1" {
		t.Errorf("control node: %+v", n)
	}
	if n := got.Nodes[1]; n.Name != "worker-1" || !n.Ready || n.Roles != "" {
		t.Errorf("worker node: %+v", n)
	}
	// Pods that need a look come first; the finished job and the healthy pod are last.
	var order []string
	for _, p := range got.Pods {
		order = append(order, p.Namespace+"/"+p.Name)
	}
	if strings.Join(order[:3], " ") != "shop/Bad_Name shop/stuck-0 shop/web-7d9f8c6b5-xk2lm" ||
		strings.Join(order[3:], " ") != "kube-system/coredns-6799fbcd5-abcde shop/backup-28000000-zz9" {
		t.Errorf("order: %v", order)
	}
	var web snapshot.KubePod
	for _, p := range got.Pods {
		if strings.HasPrefix(p.Name, "web-") {
			web = p
		}
	}
	if web.Phase != "Running" || web.Ready || web.Restarts != 7 || web.Node != "worker-1" {
		t.Errorf("web: %+v", web)
	}
	for _, p := range got.Pods {
		if p.Name == "backup-28000000-zz9" && !p.Ready {
			t.Errorf("a finished job is not a problem: %+v", p)
		}
	}
	// What the cluster keeps secret never gets into the snapshot.
	b, _ := json.Marshal(got)
	for _, leak := range []string{"must-not-leak", "DB_PASSWORD", "API_KEY", "--token", "machineID", "0123456789abcdef", "label-value", "rancher/mirrored"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("%q reached the snapshot: %s", leak, b)
		}
	}
	// Only listings are run, and nothing that reads a secret.
	for _, c := range k.calls {
		line := strings.Join(c, " ")
		listing := strings.Contains(line, "get nodes") || strings.Contains(line, "get pods --all-namespaces")
		if !strings.Contains(line, "-o json --request-timeout=3s") || strings.Contains(line, "secret") || !listing {
			t.Errorf("command: %s", line)
		}
	}
	// And after the snapshot's own checks the bad pod name is gone.
	s := &snapshot.Snapshot{Schema: 1, GeneratedAt: now0.Unix(), GeneratedMS: now0.UnixMilli(), Kubernetes: got}
	if err := s.Normalize(now0); err != nil || len(s.Kubernetes.Pods) != 4 {
		t.Errorf("normalize: %v, %d pods", err, len(s.Kubernetes.Pods))
	}
}

func TestK3sWithoutKubectlOnThePath(t *testing.T) {
	k := &kubectl{t: t, have: map[string]bool{"k3s": true}, nodes: fixture(t, "kube/nodes.json"), pods: fixture(t, "kube/pods.json")}
	got := collectKube(context.Background(), KubeProbe{Look: k.look, Run: k.run})
	if !got.Detected || got.Via != "k3s kubectl" {
		t.Fatalf("%+v", got)
	}
	if first := k.calls[0]; first[0] != "k3s" || first[1] != "kubectl" || first[2] != "get" {
		t.Errorf("command: %v", first)
	}
}

func TestNoKubernetesHere(t *testing.T) {
	none := &kubectl{t: t, have: map[string]bool{}}
	got := collectKube(context.Background(), KubeProbe{Look: none.look, Run: none.run})
	if got.Detected || got.Via != "" || !strings.Contains(got.Reason, "not found") || len(none.calls) != 0 || len(got.Nodes) != 0 {
		t.Errorf("no kubectl: %+v", got)
	}
	down := &kubectl{t: t, have: map[string]bool{"kubectl": true}, failing: true}
	got = collectKube(context.Background(), KubeProbe{Look: down.look, Run: down.run})
	if got.Detected || got.Via != "kubectl" || !strings.Contains(got.Reason, "no cluster") {
		t.Errorf("no cluster: %+v", got)
	}
	// The error text of kubectl (it names servers and users) is not kept.
	if strings.Contains(got.Reason, "exit status") {
		t.Errorf("kubectl's words were kept: %q", got.Reason)
	}
	garbled := &kubectl{t: t, have: map[string]bool{"kubectl": true}, nodes: []byte("not json")}
	if got = collectKube(context.Background(), KubeProbe{Look: garbled.look, Run: garbled.run}); got.Detected {
		t.Errorf("garbled nodes: %+v", got)
	}
	podsBroken := &kubectl{t: t, have: map[string]bool{"kubectl": true}, nodes: fixture(t, "kube/nodes.json"), pods: []byte("{")}
	got = collectKube(context.Background(), KubeProbe{Look: podsBroken.look, Run: podsBroken.run})
	if !got.Detected || len(got.Nodes) != 2 || len(got.Pods) != 0 || !strings.Contains(got.Reason, "pod list") {
		t.Errorf("garbled pods: %+v", got)
	}
}

func TestManyPodsAreCutAndTheBrokenOnesKept(t *testing.T) {
	var items []string
	for i := 0; i < 400; i++ {
		phase, ready := "Running", "True"
		if i == 399 {
			phase, ready = "Failed", "False"
		}
		items = append(items, fmt.Sprintf(`{"metadata":{"name":"pod-%03d","namespace":"ns"},"spec":{"nodeName":"n"},"status":{"phase":%q,
			"conditions":[{"type":"Ready","status":%q}]}}`, i, phase, ready))
	}
	pods := []byte(`{"items":[` + strings.Join(items, ",") + `]}`)
	k := &kubectl{t: t, have: map[string]bool{"kubectl": true}, nodes: []byte(`{"items":[]}`), pods: pods}
	got := collectKube(context.Background(), KubeProbe{Look: k.look, Run: k.run})
	if len(got.Pods) != snapshot.MaxPods || !got.Truncated || got.Pods[0].Name != "pod-399" {
		t.Errorf("%d pods, truncated %v, first %s", len(got.Pods), got.Truncated, got.Pods[0].Name)
	}
}
