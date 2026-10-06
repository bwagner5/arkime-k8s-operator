# HTTP traffic demo

Two small deployments use the stock [BusyBox image](https://hub.docker.com/_/busybox): one serves a page, and the other fetches it immediately, then waits 120 seconds between requests. Each request includes a timestamp, sequence number, and `arkime-http-demo` marker. Plain HTTP makes both sides readable in Arkime's packet viewer. No image build or external endpoint is needed.

From the repository root, deploy into a namespace of your choice:

```sh
kubectl create namespace arkime-demo
kubectl -n arkime-demo apply -f examples/http-demo.yaml
kubectl -n arkime-demo rollout status deployment/arkime-http-demo-server
kubectl -n arkime-demo logs -f deployment/arkime-http-demo-client
```

Change the delay in seconds (this restarts the client):

```sh
kubectl -n arkime-demo set env deployment/arkime-http-demo-client INTERVAL_SECONDS=30
```

Edit `INTERVAL_SECONDS` in the manifest too if you want subsequent applies to keep that value.

In Arkime, select a recent time range and search for:

```text
http.uri == *arkime-http-demo*
```

Open a matching session and expand its packets to see the GET request and HTML response, or download its PCAP. Low traffic can take time to appear or become available in the packet file because capture buffers data; temporarily shorten the interval if needed.

The client prefers a different node from the server, but scheduling may place them together. Check placement with `kubectl -n arkime-demo get pods -o wide`. Arkime must capture an interface that sees their traffic: same-node traffic may never cross `eth0`, and CNI routing or encryption affects visibility. If only some nodes run capture, schedule the demo on those nodes. Namespace network policies must allow DNS and client-to-server TCP 8080.

Remove the demo when finished:

```sh
kubectl -n arkime-demo delete -f examples/http-demo.yaml
```
