//go:build e2e

package e2e

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func command(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	b, e := exec.Command(name, args...).CombinedOutput()
	if e != nil {
		t.Fatalf("%s failed: %v\n%s", name, e, b)
	}
	return b
}
func kube(t *testing.T, args ...string) []byte {
	return command(t, "kubectl", append([]string{"--context", os.Getenv("E2E_CONTEXT"), "-n", "arkime"}, args...)...)
}
func TestTZSPPersistenceAndAuthentication(t *testing.T) {
	if os.Getenv("E2E_CONTEXT") == "" {
		t.Fatal("run with make test-e2e")
	}
	kube(t, "patch", "arkimecluster", "demo", "--type=merge", "-p", `{"spec":{"capture":{"external":{"enabled":true}}}}`)
	time.Sleep(5 * time.Second)
	kube(t, "wait", "arkimecluster/demo", "--for=condition=Ready", "--timeout=180s")
	var cr struct {
		Status struct {
			AdminSecret string `json:"adminSecret"`
		}
	}
	if err := json.Unmarshal(kube(t, "get", "arkimecluster", "demo", "-o", "json"), &cr); err != nil {
		t.Fatal(err)
	}
	secret := map[string]any{}
	if err := json.Unmarshal(kube(t, "get", "secret", cr.Status.AdminSecret, "-o", "json"), &secret); err != nil {
		t.Fatal(err)
	}
	data := secret["data"].(map[string]any)
	u, _ := base64.StdEncoding.DecodeString(data["username"].(string))
	p, _ := base64.StdEncoding.DecodeString(data["password"].(string))
	service := strings.TrimSpace(string(kube(t, "get", "svc", "-l", "app.kubernetes.io/component=viewer", "-o", "jsonpath={.items[0].metadata.name}")))
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	forward := exec.Command("kubectl", "--context", os.Getenv("E2E_CONTEXT"), "-n", "arkime", "port-forward", "service/"+service, fmt.Sprintf("%d:8005", port))
	var logs bytes.Buffer
	forward.Stdout = &logs
	forward.Stderr = &logs
	if e = forward.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = forward.Process.Kill(); _ = forward.Wait() }()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("port forward not ready")
		}
		time.Sleep(time.Second)
	}
	// Use curl config through stdin to keep the password out of process arguments/logs.
	get := func(path string) []byte {
		t.Helper()
		cmd := exec.Command("curl", "--silent", "--show-error", "--fail", "--max-time", "30", "--digest", "--config", "-", base+path)
		cmd.Stdin = strings.NewReader(fmt.Sprintf("user = %q\n", string(u)+":"+string(p)))
		out, err := cmd.Output()
		if err != nil {
			// Config changes roll the central viewer; reconnect the port-forward to the new pod.
			_ = forward.Process.Kill()
			_ = forward.Wait()
			forward = exec.Command("kubectl", "--context", os.Getenv("E2E_CONTEXT"), "-n", "arkime", "port-forward", "service/"+service, fmt.Sprintf("%d:8005", port))
			if e := forward.Start(); e != nil {
				t.Fatal(e)
			}
			time.Sleep(2 * time.Second)
			retry := exec.Command("curl", "--silent", "--show-error", "--fail", "--digest", "--max-time", "30", "--config", "-", base+path)
			retry.Stdin = strings.NewReader(fmt.Sprintf("user = %q\n", string(u)+":"+string(p)))
			out, err = retry.Output()
			if err != nil {
				t.Fatalf("authenticated viewer request failed: %v", err)
			}
		}
		return out
	}
	unauthenticated := string(command(t, "curl", "--silent", "--output", "/dev/null", "--write-out", "%{http_code}", "--max-time", "10", base+"/api/sessions"))
	if unauthenticated != "401" {
		t.Fatalf("unauthenticated sessions returned %s", unauthenticated)
	}
	packet := make([]byte, 14+20+8+16)
	copy(packet[:12], []byte{0, 1, 2, 3, 4, 5, 0, 6, 7, 8, 9, 10})
	binary.BigEndian.PutUint16(packet[12:14], 0x0800)
	ip := packet[14:34]
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(packet)-14))
	ip[8] = 64
	ip[9] = 17
	copy(ip[12:16], []byte{198, 18, 0, 1})
	copy(ip[16:20], []byte{198, 18, 0, 2})
	sum := uint32(0)
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(ip[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(ip[10:12], ^uint16(sum))
	sourcePort := uint16(40000 + time.Now().UnixNano()%20000)
	binary.BigEndian.PutUint16(packet[34:36], sourcePort)
	binary.BigEndian.PutUint16(packet[36:38], 42043)
	binary.BigEndian.PutUint16(packet[38:40], 24)
	copy(packet[42:], []byte("arkime-e2e-proof"))
	tzsp := append([]byte{1, 0, 0, 1, 1}, packet...)
	pod := strings.TrimSpace(string(kube(t, "get", "pods", "-l", "app.kubernetes.io/component=external", "-o", "jsonpath={.items[0].metadata.name}")))
	udp := strings.TrimSpace(string(kube(t, "get", "svc", "-l", "app.kubernetes.io/component=udp", "-o", "jsonpath={.items[0].metadata.name}")))
	// Fill at least one compressed writer page; a single low-rate packet stays buffered in Arkime 6.7.
	js := fmt.Sprintf("const s=require('dgram').createSocket('udp4');const b=Buffer.from('%s','base64');let n=0;function send(){s.send(b,37008,'%s',err=>{if(err)throw err;if(++n===2048)return s.close();require('crypto').randomFillSync(b,47);setTimeout(send,1);});}send();", base64.StdEncoding.EncodeToString(tzsp), udp)
	kube(t, "exec", pod, "-c", "capture", "--", "/opt/arkime/bin/node", "-e", js)
	query := "?date=-1&expression=" + url.QueryEscape(fmt.Sprintf("ip.src == 198.18.0.1 && port.src == %d", sourcePort))
	deadline = time.Now().Add(3 * time.Minute)
	for {
		var sessions struct {
			RecordsFiltered int `json:"recordsFiltered"`
		}
		if err := json.Unmarshal(get("/api/sessions"+query), &sessions); err != nil {
			t.Fatal(err)
		}
		if sessions.RecordsFiltered > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TZSP packet did not become searchable")
		}
		time.Sleep(5 * time.Second)
	}
	checkPCAP := func() {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			pcap := get("/api/sessions.pcap" + query)
			if bytes.Contains(pcap, packet) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("downloaded PCAP (%d bytes) does not contain the exact injected packet", len(pcap))
			}
			time.Sleep(2 * time.Second)
		}
	}
	checkPCAP()
	kube(t, "delete", "pod", pod, "--wait=true")
	kube(t, "rollout", "status", "deployment/"+strings.TrimSpace(string(kube(t, "get", "deploy", "-l", "app.kubernetes.io/component=external", "-o", "jsonpath={.items[0].metadata.name}"))), "--timeout=180s")
	checkPCAP()
	kube(t, "patch", "arkimecluster", "demo", "--type=merge", "-p", `{"spec":{"capture":{"external":{"enabled":false}}}}`)
	time.Sleep(5 * time.Second)
	kube(t, "wait", "arkimecluster/demo", "--for=condition=Ready", "--timeout=180s")
	checkPCAP()
	containers := string(kube(t, "get", "deploy", "-l", "app.kubernetes.io/component=external", "-o", "jsonpath={.items[0].spec.template.spec.containers[*].name}"))
	if containers != "local-viewer" {
		t.Fatalf("disabled capture still has containers: %s", containers)
	}
	// The fixture also installs two node sensors with an overridden local-viewer port.
	var nodes struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			}
			Spec struct {
				NodeName string `json:"nodeName"`
			}
			Status struct {
				HostIP string `json:"hostIP"`
			}
		}
	}
	if err := json.Unmarshal(kube(t, "get", "pods", "-l", "app.kubernetes.io/component=node", "-o", "json"), &nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes.Items) != 2 {
		t.Fatalf("expected two Linux node sensors, got %d", len(nodes.Items))
	}
	for i, node := range nodes.Items {
		destination := nodes.Items[1-i].Status.HostIP
		sourcePort := 40000 + int(time.Now().UnixNano()%15000)
		marker := fmt.Sprintf("arkime-node-%d-proof", i)
		sender := fmt.Sprintf("const s=require('dgram').createSocket('udp4');const b=Buffer.alloc(1200);s.bind(%d,()=>{let n=0;function send(){require('crypto').randomFillSync(b);b.write('%s');s.send(b,45999,'%s',err=>{if(err)throw err;if(++n===1024)return s.close();setTimeout(send,1);});}send();});", sourcePort, marker, destination)
		kube(t, "exec", node.Metadata.Name, "-c", "local-viewer", "--", "/opt/arkime/bin/node", "-e", sender)
		q := "?date=-1&expression=" + url.QueryEscape(fmt.Sprintf("port.src == %d && node == %s-%s", sourcePort, node.Metadata.Labels["arkime.arkime.com/cluster"], node.Spec.NodeName))
		deadline := time.Now().Add(3 * time.Minute)
		for {
			var found struct {
				RecordsFiltered int `json:"recordsFiltered"`
			}
			if err := json.Unmarshal(get("/api/sessions"+q), &found); err != nil {
				t.Fatal(err)
			}
			if found.RecordsFiltered > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("node capture session missing")
			}
			time.Sleep(5 * time.Second)
		}
		if !bytes.Contains(get("/api/sessions.pcap"+q), []byte(marker)) {
			t.Fatal("node PCAP retrieval missing marker")
		}
		kube(t, "delete", "pod", node.Metadata.Name, "--wait=true", "--timeout=150s")
		ds := strings.TrimSpace(string(kube(t, "get", "daemonset", "-o", "jsonpath={.items[0].metadata.name}")))
		kube(t, "rollout", "status", "daemonset/"+ds, "--timeout=180s")
		if !bytes.Contains(get("/api/sessions.pcap"+q), []byte(marker)) {
			t.Fatal("historical node PCAP missing after pod replacement")
		}
	}
	var enriched struct {
		RecordsFiltered int `json:"recordsFiltered"`
	}
	if err := json.Unmarshal(get("/api/sessions?date=-1&expression="+url.QueryEscape("tags == e2e-known-ip")), &enriched); err != nil {
		t.Fatal(err)
	}
	if enriched.RecordsFiltered == 0 {
		t.Fatal("WISE fixture did not enrich captured sessions")
	}

}
