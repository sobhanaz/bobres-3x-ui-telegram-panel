package collect

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// BOBRES runs on Docker Compose, not Kubernetes. When the host also has
// kubectl (or k3s, which carries its own), the collector lists the cluster's
// nodes and pods so the dashboard can show them; where there is no cluster it
// says so and nothing more. It runs `get nodes` and `get pods`, nothing that
// reads a secret, and keeps a pod's name, namespace, phase, readiness,
// restarts and node: not its containers, environment or labels.

const (
	kubeTimeout = 3 * time.Second
	// maxKubeOutput bounds a listing: 200 pods with their specs are a few megabytes.
	maxKubeOutput = 8 << 20
)

// KubeProbe runs kubectl: the program found (kubectl, else k3s), and how.
type KubeProbe struct {
	// Look finds a program (LookTool).
	Look func(string) (string, error)
	// Run runs it, keeping at most maxKubeOutput bytes of output.
	Run Runner
}

func collectKube(ctx context.Context, p KubeProbe) *snapshot.Kube {
	var via string
	var base []string
	var prog string
	switch {
	case lookOK(p.Look, "kubectl"):
		prog, via = "kubectl", "kubectl"
	case lookOK(p.Look, "k3s"):
		prog, via, base = "k3s", "k3s kubectl", []string{"kubectl"}
	default:
		return &snapshot.Kube{Reason: "kubectl and k3s were not found on this host"}
	}
	ask := func(args ...string) ([]byte, error) {
		cctx, cancel := context.WithTimeout(ctx, kubeTimeout)
		defer cancel()
		all := append(slices.Clone(base), args...)
		all = append(all, "-o", "json", "--request-timeout=3s")
		return p.Run(cctx, prog, all...)
	}
	nodesRaw, err := ask("get", "nodes")
	if err != nil {
		return &snapshot.Kube{Via: via, Reason: "no cluster answered (is a kubeconfig set up for root?)"}
	}
	nodes, err := parseNodes(nodesRaw)
	if err != nil {
		return &snapshot.Kube{Via: via, Reason: "the node list could not be read"}
	}
	k := &snapshot.Kube{Detected: true, Via: via, Nodes: nodes}
	podsRaw, err := ask("get", "pods", "--all-namespaces")
	if err != nil {
		k.Reason = "the pod list could not be read"
		return k
	}
	pods, err := parsePods(podsRaw)
	if err != nil {
		k.Reason = "the pod list could not be read"
		return k
	}
	k.Pods = pods
	if len(k.Pods) > snapshot.MaxPods {
		k.Pods, k.Truncated = k.Pods[:snapshot.MaxPods], true
	}
	return k
}

func lookOK(look func(string) (string, error), name string) bool {
	_, err := look(name)
	return err == nil
}

// parseNodes reads `kubectl get nodes -o json`.
func parseNodes(raw []byte) ([]snapshot.KubeNode, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				NodeInfo struct {
					Kubelet string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	var out []snapshot.KubeNode
	for _, it := range list.Items {
		n := snapshot.KubeNode{Name: it.Metadata.Name, Version: it.Status.NodeInfo.Kubelet}
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" {
				n.Ready = c.Status == "True"
			}
		}
		var roles []string
		for k := range it.Metadata.Labels {
			if r, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok && r != "" {
				roles = append(roles, r)
			}
		}
		sort.Strings(roles)
		n.Roles = strings.Join(roles, ",")
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b snapshot.KubeNode) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// parsePods reads `kubectl get pods -A -o json`, the pods that need a look
// first (not ready, restarting), then by namespace and name.
func parsePods(raw []byte) ([]snapshot.KubePod, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Node string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase      string `json:"phase"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				Containers []struct {
					Ready    bool `json:"ready"`
					Restarts int  `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make([]snapshot.KubePod, 0, len(list.Items))
	for _, it := range list.Items {
		p := snapshot.KubePod{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name, Phase: it.Status.Phase, Node: it.Spec.Node}
		allReady := len(it.Status.Containers) > 0
		for _, c := range it.Status.Containers {
			p.Restarts += c.Restarts
			allReady = allReady && c.Ready
		}
		p.Ready = allReady
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" {
				p.Ready = c.Status == "True"
			}
		}
		if p.Phase == "Succeeded" {
			p.Ready = true // a finished job is not a problem
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b snapshot.KubePod) int {
		if pa, pb := podProblem(a), podProblem(b); pa != pb {
			if pa {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func podProblem(p snapshot.KubePod) bool {
	return !p.Ready || p.Phase == "Failed" || p.Phase == "Pending" || p.Phase == "Unknown"
}
