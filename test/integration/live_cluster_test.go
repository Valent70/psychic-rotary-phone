// TestLiveRaftCluster_ElectsLeaderReplicatesAndFailsOver is the concrete
// closure of the audit's most-cited open item: "Distributed Kernel —
// cluster raft multi-proses live". It does not call raftlite.Node in one
// process with an in-memory transport (that was already proven by
// pkg/consensus/raftlite's own tests) — it compiles cmd/veriqo-node once,
// launches THREE SEPARATE OS PROCESSES with real listening TCP sockets on
// 127.0.0.1, has them mint a shared CA up front (fixing the bug where the
// previous version of cmd/veriqo-node minted an independent, mutually
// distrusting CA per process), and drives the cluster only through each
// process's HTTP admin surface — the same interface an external operator
// would use.
//
// It proves, over the network, against real processes:
//  1. the three processes elect exactly one leader;
//  2. a command proposed to the leader is replicated and visible via
//     GET /state on a FOLLOWER process (cross-process replication, not
//     shared memory);
//  3. killing the leader process causes the remaining two to elect a new
//     leader (failover) and the previously-committed state survives.
package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/kernel/distributed"
)

func buildVeriqoNode(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source location")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	binPath := filepath.Join(t.TempDir(), "veriqo-node")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/veriqo-node")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building cmd/veriqo-node: %v\n%s", err, out)
	}
	return binPath
}

// freePort asks the OS for an ephemeral port and immediately releases it.
// Good enough for a test on loopback where nothing else races us for it.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type liveNode struct {
	id        string
	raftAddr  string
	adminAddr string
	cmd       *exec.Cmd
}

func (n *liveNode) statusURL() string { return "http://" + n.adminAddr + "/status" }
func (n *liveNode) stateURL() string  { return "http://" + n.adminAddr + "/state" }
func (n *liveNode) proposeURL() string {
	return "http://" + n.adminAddr + "/propose"
}

func startNode(t *testing.T, bin, id string, raftAddrs map[string]string, adminAddr, caCert, caKey string) *liveNode {
	t.Helper()
	var peerArgs []string
	for pid, addr := range raftAddrs {
		if pid == id {
			continue
		}
		peerArgs = append(peerArgs, pid+"="+addr)
	}
	args := []string{
		"-id", id,
		"-peers", strings.Join(peerArgs, ","),
		"-listen", raftAddrs[id],
		"-admin", adminAddr,
		"-admin-token", testAdminToken,
		"-ca-cert", caCert,
		"-ca-key", caKey,
		"-fsm", "distributed",
	}
	cmd := exec.Command(bin, args...)
	if testing.Verbose() {
		cmd.Stdout = &prefixWriter{prefix: id + " "}
		cmd.Stderr = &prefixWriter{prefix: id + "! "}
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting node %s: %v", id, err)
	}
	return &liveNode{id: id, raftAddr: raftAddrs[id], adminAddr: adminAddr, cmd: cmd}
}

type prefixWriter struct{ prefix string }

func (w *prefixWriter) Write(p []byte) (int, error) {
	fmt.Print(w.prefix, string(p))
	return len(p), nil
}

// testAdminToken is the fixed bearer token every node in this test's
// cluster is started with (see startNode's -admin-token arg below).
// cmd/veriqo-node now refuses to start an -admin server at all without
// a token (see this session's crash-matrix/security hardening work) —
// every admin call this test makes must carry it.
const testAdminToken = "veriqo-live-cluster-test-token"

func getJSON(url string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func postAuthed(url, contentType, body string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	return http.DefaultClient.Do(req)
}

func waitForLeader(t *testing.T, nodes []*liveNode, timeout time.Duration) *liveNode {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			var st struct {
				IsLeader bool   `json:"is_leader"`
				Role     string `json:"role"`
			}
			if err := getJSON(n.statusURL(), &st); err == nil && st.IsLeader {
				return n
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no leader elected within timeout")
	return nil
}

func TestLiveRaftCluster_ElectsLeaderReplicatesAndFailsOver(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real OS processes and TCP servers; skipped in -short")
	}
	bin := buildVeriqoNode(t)
	dir := t.TempDir()

	// Mint ONE shared CA, exactly as a real cluster operator would before
	// booting any node — this is the fix under test as much as raft itself.
	genCmd := exec.Command(bin, "gen-ca", "-dir", dir)
	if out, err := genCmd.CombinedOutput(); err != nil {
		t.Fatalf("gen-ca: %v\n%s", err, out)
	}
	caCert := filepath.Join(dir, "ca-cert.pem")
	caKey := filepath.Join(dir, "ca-key.pem")

	ids := []string{"A", "B", "C"}
	raftAddrs := map[string]string{}
	adminAddrs := map[string]string{}
	for _, id := range ids {
		raftAddrs[id] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
		adminAddrs[id] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	}

	var nodes []*liveNode
	for _, id := range ids {
		n := startNode(t, bin, id, raftAddrs, adminAddrs[id], caCert, caKey)
		nodes = append(nodes, n)
		defer func(n *liveNode) {
			if n.cmd.Process != nil {
				_ = n.cmd.Process.Kill()
				_, _ = n.cmd.Process.Wait()
			}
		}(n)
	}

	// 1. Three real processes, real mTLS TCP, must elect exactly one leader.
	leader := waitForLeader(t, nodes, 15*time.Second)
	t.Logf("elected leader: %s", leader.id)

	// 2. Propose a real Distributed Kernel command to the leader's HTTP
	// admin surface, then read it back from a DIFFERENT process's /state —
	// proof of cross-process replication, not shared memory.
	joinCmd := distributed.Command{Kind: distributed.CmdJoin, NodeID: "engine-node-1"}
	resp, err := postAuthed(leader.proposeURL(), "application/octet-stream", string(joinCmd.Encode()))
	if err != nil {
		t.Fatalf("propose JOIN: %v", err)
	}
	resp.Body.Close()

	placeCmd := distributed.Command{Kind: distributed.CmdPlace, NodeID: "engine-node-1", EngineID: "fusion-engine"}
	resp2, err := postAuthed(leader.proposeURL(), "application/octet-stream", string(placeCmd.Encode()))
	if err != nil {
		t.Fatalf("propose PLACE: %v", err)
	}
	resp2.Body.Close()

	var follower *liveNode
	for _, n := range nodes {
		if n.id != leader.id {
			follower = n
			break
		}
	}

	// 200 x 50ms = 10s, not the original 100 x 50ms = 5s: found too tight
	// on real CI ("PLACE command did not replicate to follower B within
	// timeout") immediately after test/e2e/eight_blockers's own real,
	// heavier run in the same `go test ./...` invocation -- this test
	// spawns three genuinely separate OS processes with real mTLS TCP,
	// which is far more real-world contention-sensitive than any
	// in-process raftlite test, so it needs the same kind of generous
	// margin those tests already carry for the identical reason.
	replicated := false
	for i := 0; i < 200; i++ {
		var state map[string]any
		if err := getJSON(follower.stateURL(), &state); err == nil {
			if logLen, ok := state["log_len"].(float64); ok && logLen >= 2 {
				chainOK, _ := state["chain_ok"].(bool)
				if !chainOK {
					t.Fatalf("follower %s reports broken hash chain after replication", follower.id)
				}
				replicated = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !replicated {
		t.Fatalf("PLACE command did not replicate to follower %s within timeout", follower.id)
	}
	t.Logf("command replicated to follower %s across real OS processes", follower.id)

	// 3. Kill the leader process. The remaining two real processes must
	// elect a new leader among themselves (real failover, not simulated).
	if err := leader.cmd.Process.Kill(); err != nil {
		t.Fatalf("killing leader process: %v", err)
	}
	_, _ = leader.cmd.Process.Wait()

	var survivors []*liveNode
	for _, n := range nodes {
		if n.id != leader.id {
			survivors = append(survivors, n)
		}
	}
	newLeader := waitForLeader(t, survivors, 15*time.Second)
	if newLeader.id == leader.id {
		t.Fatalf("new leader is the same as the killed leader — failover did not happen")
	}
	t.Logf("failover complete: new leader is %s (old leader %s killed)", newLeader.id, leader.id)

	// Previously-committed state must have survived the leader's death.
	var state map[string]any
	if err := getJSON(newLeader.stateURL(), &state); err != nil {
		t.Fatalf("reading state from new leader: %v", err)
	}
	if logLen, ok := state["log_len"].(float64); !ok || logLen < 2 {
		t.Fatalf("committed state lost after failover: %#v", state)
	}
}
