package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	api "github.com/bwagner5/arkime-k8s-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var token = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)
var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
var owned = strings.Fields("cronQueries insecure authDiscoverURL authClientId authClientSecret authRedirectURIs authUserIdField elasticsearch elasticsearchBasicAuth elasticsearchAPIKey usersElasticsearch usersElasticsearchBasicAuth usersElasticsearchAPIKey prefix usersPrefix passwordSecret serverSecret authMode userNameHeader pcapDir pcapReadMethod interface nodeClass viewPort viewHost viewUrl centralViewer wiseURL webBasePath cont3xtURL arkimeUrl arkimeWebURL caTrustFile esClientKey esClientCert freeSpaceG rotateIndex tzspPort tcpHealthCheckPort dropUser dropGroup plugins viewerPlugins")

func Hash(v string) string           { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:])[:12] }
func ID(c *api.ArkimeCluster) string { return "ak-" + Hash(c.Namespace+"/"+c.Name) }
func Name(c *api.ArkimeCluster, component string) string {
	n := c.Name
	if len(n) > 35 {
		n = n[:35]
	}
	return n + "-" + Hash(c.Namespace + "/" + c.Name)[:6] + "-" + component
}
func Prefix(c *api.ArkimeCluster) string {
	if c.Spec.Database.IndexPrefix != "" {
		return strings.TrimSuffix(c.Spec.Database.IndexPrefix, "_") + "_"
	}
	return "ak_" + Hash(c.Namespace+"/"+c.Name) + "_"
}
func Port(c *api.ArkimeCluster) int32 {
	if c.Spec.Capture.Node != nil && c.Spec.Capture.Node.ViewerPort != 0 {
		return c.Spec.Capture.Node.ViewerPort
	}
	return 8005
}
func ServiceURL(c *api.ArkimeCluster, component string, port int) string {
	return fmt.Sprintf("http://%s.%s.svc:%d", Name(c, component), c.Namespace, port)
}
func ViewerURL(c *api.ArkimeCluster) string {
	if c.Spec.Web.Host != "" {
		scheme := "http"
		if c.Spec.Web.TLSSecret != "" || c.Spec.Web.Mode == "Gateway" {
			scheme = "https"
		}
		return scheme + "://" + c.Spec.Web.Host
	}
	return ServiceURL(c, "viewer", 8005)
}
func Components(c *api.ArkimeCluster) map[string]api.ComponentSpec {
	m := map[string]api.ComponentSpec{"viewer": c.Spec.Viewer}
	if api.Enabled(c.Spec.Cont3xt.ComponentSpec) {
		m["cont3xt"] = c.Spec.Cont3xt.ComponentSpec
	}
	if api.Enabled(c.Spec.Wise.ComponentSpec) {
		m["wise"] = c.Spec.Wise.ComponentSpec
	}
	if c.Spec.Capture.Node != nil {
		m["node"] = c.Spec.Capture.Node.ComponentSpec
	}
	if c.Spec.Capture.External != nil {
		m["external"] = c.Spec.Capture.External.ComponentSpec
	}
	return m
}
func Backends(c *api.ArkimeCluster) map[string]api.Backend {
	m := map[string]api.Backend{"primary": c.Spec.Database.Backend}
	if c.Spec.Database.Users != nil {
		m["users"] = c.Spec.Database.Users.Backend
	}
	if api.Enabled(c.Spec.Cont3xt.ComponentSpec) && c.Spec.Cont3xt.Database != nil {
		m["cont3xt"] = *c.Spec.Cont3xt.Database
	}
	return m
}
func Validate(c *api.ArkimeCluster) error {
	if err := validateEnrichment(c); err != nil {
		return err
	}
	if c.Spec.Database.TLS.ClientCertificateSecret != "" && api.Enabled(c.Spec.Cont3xt.ComponentSpec) {
		return fmt.Errorf("Cont3xt Arkime integration does not support database mTLS; disable Cont3xt")
	}
	for name, b := range Backends(c) {
		if name != "primary" && b.TLS.ClientCertificateSecret != "" && b.TLS.ClientCertificateSecret != c.Spec.Database.TLS.ClientCertificateSecret {
			return fmt.Errorf("distinct backend client certificates are not supported by this image")
		}
	}
	if !regexp.MustCompile(`^([0-9]+(\.[0-9]+)?%?)?$`).MatchString(c.Spec.Retention.PcapFreeSpace) {
		return fmt.Errorf("pcapFreeSpace must be gigabytes or a percentage")
	}
	if c.Spec.Viewer.Enabled != nil && !*c.Spec.Viewer.Enabled {
		return fmt.Errorf("viewer is required")
	}
	if (c.Spec.Capture.Node == nil || !api.Enabled(c.Spec.Capture.Node.ComponentSpec)) && (c.Spec.Capture.External == nil || !api.Enabled(c.Spec.Capture.External.ComponentSpec)) && !c.Status.NodeStorageRetained && !c.Status.ExternalStorageRetained {
		return fmt.Errorf("initial installation requires at least one enabled capture mode")
	}
	if c.Spec.Capture.Node == nil && c.Spec.Capture.External == nil {
		return fmt.Errorf("at least one capture block is required")
	}
	if c.Spec.Retention.Mode != "External" && c.Spec.Retention.SessionsDays < 1 {
		return fmt.Errorf("retention.sessionsDays is required for managed expiry")
	}
	for name, b := range Backends(c) {
		if b.Engine != "OpenSearch" && b.Engine != "Elasticsearch" {
			return fmt.Errorf("%s: invalid database engine", name)
		}
		if len(b.Endpoints) == 0 {
			return fmt.Errorf("%s: endpoints required", name)
		}
		for _, s := range b.Endpoints {
			u, e := url.Parse(s)
			if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(s, "\r\n;") {
				return fmt.Errorf("%s: endpoint must be an HTTP(S) URL without credentials, query or fragment", name)
			}
		}
		n := 0
		if b.Auth.BasicAuthSecretRef != nil {
			n++
		}
		if b.Auth.APIKeySecretRef != nil {
			n++
		}
		if b.Auth.Unauthenticated {
			n++
		}
		if n != 1 {
			return fmt.Errorf("%s: choose exactly one authentication method", name)
		}
	}
	if !prefixPattern.MatchString(strings.TrimSuffix(Prefix(c), "_")) {
		return fmt.Errorf("invalid database index prefix")
	}
	if u := c.Spec.Database.Users; u != nil && !prefixPattern.MatchString(strings.TrimSuffix(u.IndexPrefix, "_")) {
		return fmt.Errorf("invalid users index prefix")
	}
	if n := c.Spec.Capture.Node; n != nil {
		if strings.ContainsAny(n.BPF, "\r\n\x00") {
			return fmt.Errorf("BPF must be a single line")
		}

		if n.Storage.HostPath == "/" || !filepath.IsAbs(n.Storage.HostPath) || filepath.Clean(n.Storage.HostPath) != n.Storage.HostPath {
			return fmt.Errorf("node storage requires a dedicated absolute clean hostPath")
		}
		if api.Enabled(n.ComponentSpec) && len(n.Interfaces) == 0 {
			return fmt.Errorf("enabled node capture requires interfaces")
		}
		for _, i := range n.Interfaces {
			if !token.MatchString(i) {
				return fmt.Errorf("invalid interface")
			}
		}
		if Port(c) < 1024 || Port(c) > 65534 {
			return fmt.Errorf("invalid node viewer port")
		}
	}
	if e := c.Spec.Capture.External; e != nil {
		if (e.Storage.ExistingClaim != "") == (e.Storage.VolumeClaim != nil) {
			return fmt.Errorf("choose exactly one PVC source")
		}
		if v := e.Storage.VolumeClaim; v != nil {
			q, err := resource.ParseQuantity(v.Size)
			if err != nil || q.Sign() <= 0 {
				return fmt.Errorf("invalid PVC size")
			}
		}
		switch e.Exposure.Mode {
		case "ClusterIP", "LoadBalancer":
			if e.Exposure.Gateway != nil {
				return fmt.Errorf("gateway requires Gateway exposure")
			}
		case "Gateway":
			g := e.Exposure.Gateway
			if g == nil || (g.GatewayClassName != "") == (g.ParentRef != nil) {
				return fmt.Errorf("Gateway exposure requires exactly one class or parent")
			}
		default:
			return fmt.Errorf("invalid UDP exposure mode")
		}
	}
	if c.Spec.Web.Mode != "" && c.Spec.Web.Mode != "ClusterIP" {
		if c.Spec.Web.Host == "" || strings.ContainsAny(c.Spec.Web.Host, "/\r\n: ") {
			return fmt.Errorf("web exposure requires a DNS host")
		}
		if c.Spec.Web.Mode == "Gateway" && c.Spec.Web.ParentRef == nil {
			return fmt.Errorf("web Gateway requires parentRef")
		}
	}
	if o := c.Spec.Auth.OIDC; o != nil {
		u, err := url.Parse(o.DiscoverURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || o.ClientID == "" || c.Spec.Web.Host == "" || (c.Spec.Web.Mode != "Gateway" && c.Spec.Web.TLSSecret == "") {
			return fmt.Errorf("OIDC requires HTTPS discovery, client registration and HTTPS public web exposure")
		}
	}
	for _, comp := range Components(c) {
		for _, p := range append(append([]string{}, comp.Plugins...), comp.ViewerPlugins...) {
			if !token.MatchString(p) {
				return fmt.Errorf("invalid plugin name")
			}
		}
		seen := map[string]bool{}
		for _, ref := range comp.ConfigSecretRefs {
			key := ref.Section + "/" + ref.Key
			if seen[key] {
				return fmt.Errorf("duplicate secret setting %s", key)
			}
			seen[key] = true
			if _, ok := comp.Config[ref.Section][ref.Key]; ok {
				return fmt.Errorf("setting %s appears in config and configSecretRefs", key)
			}
		}

		for section, kv := range comp.Config {
			for k, v := range kv {
				if err := ValidateSetting(section, k, v, false); err != nil {
					return err
				}
			}
		}
		for _, r := range comp.ConfigSecretRefs {
			if err := ValidateSetting(r.Section, r.Key, "", true); err != nil {
				return err
			}
		}
	}
	return nil
}
func ValidateSetting(section, key, value string, secret bool) error {
	if !token.MatchString(section) || !token.MatchString(key) || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("invalid INI section/key/value")
	}
	for _, k := range owned {
		if strings.EqualFold(k, key) {
			return fmt.Errorf("setting %s is owned by the operator", key)
		}
	}
	if !secret && (strings.Contains(strings.ToLower(key), "password") || strings.Contains(strings.ToLower(key), "secret") || strings.Contains(strings.ToLower(key), "apikey")) {
		return fmt.Errorf("%s must use configSecretRefs", key)
	}
	return nil
}
func Render(sections map[string]map[string]string) string {
	var b strings.Builder
	names := make([]string, 0, len(sections))
	for n := range sections {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "[%s]\n", n)
		keys := make([]string, 0, len(sections[n]))
		for k := range sections[n] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%s\n", k, sections[n][k])
		}
		b.WriteByte('\n')
	}
	return b.String()
}
func Sections(c *api.ArkimeCluster, component string) map[string]map[string]string {
	s := map[string]map[string]string{}
	for sec, kv := range Components(c)[component].Config {
		s[sec] = map[string]string{}
		for k, v := range kv {
			s[sec][k] = v
		}
	}
	d := map[string]string{"elasticsearch": strings.Join(c.Spec.Database.Endpoints, ";"), "prefix": Prefix(c), "usersElasticsearch": strings.Join(c.Spec.Database.Endpoints, ";"), "usersPrefix": Prefix(c), "authMode": "digest", "pcapDir": "/opt/arkime/raw", "viewPort": strconv.Itoa(int(Port(c))), "rotateIndex": "daily", "freeSpaceG": "10%", "viewHost": "0.0.0.0", "dropUser": "nobody", "dropGroup": "nogroup"}
	if u := c.Spec.Database.Users; u != nil {
		d["usersElasticsearch"] = strings.Join(u.Endpoints, ";")
		d["usersPrefix"] = u.IndexPrefix
	}
	if c.Spec.Retention.PcapFreeSpace != "" {
		d["freeSpaceG"] = c.Spec.Retention.PcapFreeSpace
	}
	if anyCA(c) {
		d["caTrustFile"] = "/var/run/arkime-ca/bundle/ca.crt"
	}
	if c.Spec.Database.TLS.ClientCertificateSecret != "" {
		d["esClientCert"] = "/var/run/arkime-mtls/tls.crt"
		d["esClientKey"] = "/var/run/arkime-mtls/tls.key"
	}
	if api.Enabled(c.Spec.Wise.ComponentSpec) {
		d["plugins"] = "wise.so"
		d["wiseURL"] = ServiceURL(c, "wise", 8081)
		d["viewerPlugins"] = "wise.js"
	}
	if component == "node" {
		n := c.Spec.Capture.Node
		if _, ok := s["default"]["tpacketv3BlockSize"]; !ok {
			d["tpacketv3BlockSize"] = "65536"
		}
		if _, ok := s["default"]["tpacketv3NumThreads"]; !ok {
			d["tpacketv3NumThreads"] = "1"
		}
		d["interface"] = strings.Join(n.Interfaces, ";")
		d["pcapReadMethod"] = "tpacketv3"
		d["nodeClass"] = ID(c) + "-node"
		if n.BPF != "" {
			d["bpf"] = n.BPF
		}
	}
	if component == "external" {
		d["interface"] = "dummy"
		d["pcapReadMethod"] = "tzsp"
		d["tzspPort"] = "37008"
		d["viewPort"] = "8005"
	}
	if component == "node" || component == "external" {
		d["plugins"] = strings.Trim(d["plugins"]+";tcphealthcheck.so", ";")
		d["tcpHealthCheckPort"] = "8006"
		if component == "node" {
			d["tcpHealthCheckPort"] = strconv.Itoa(int(Port(c) + 1))
		}
	}
	comp := Components(c)[component]
	d["plugins"] = mergePlugins(d["plugins"], comp.Plugins)
	d["viewerPlugins"] = mergePlugins(d["viewerPlugins"], comp.ViewerPlugins)
	if o := c.Spec.Auth.OIDC; o != nil && (component == "viewer" || component == "cont3xt") {
		d["authMode"] = "oidc"
		d["authDiscoverURL"] = o.DiscoverURL
		d["authClientId"] = o.ClientID
		d["authUserIdField"] = o.UserIDField
		if d["authUserIdField"] == "" {
			d["authUserIdField"] = "sub"
		}
		base := ViewerURL(c)
		if component == "cont3xt" {
			base += "/cont3xt"
		}
		d["authRedirectURIs"] = base + "/auth/login/callback"
	}
	if component == "viewer" {
		d["pcapDir"] = ""
		d["cronQueries"] = "true"
		d["viewPort"] = strconv.Itoa(int(Port(c)))
		s[ID(c)+"-viewer"] = map[string]string{"viewPort": "8005"}
		d["arkimeWebURL"] = ViewerURL(c)
		s[ID(c)+"-node"] = map[string]string{"viewPort": strconv.Itoa(int(Port(c)))}
		s[ID(c)+"-external"] = map[string]string{"viewPort": "8005"}
	}
	if api.Enabled(c.Spec.Cont3xt.ComponentSpec) {
		d["cont3xtURL"] = ServiceURL(c, "cont3xt", 3218)
		if c.Spec.Web.Host != "" {
			d["cont3xtURL"] = ViewerURL(c) + "/cont3xt/"
		}
	}
	if s["default"] == nil {
		s["default"] = map[string]string{}
	}
	for k, v := range d {
		s["default"][k] = v
	}
	if component == "cont3xt" {
		s["cont3xt"] = clone(s["cont3xt"])
		for _, k := range []string{"elasticsearch", "usersElasticsearch", "usersPrefix", "authMode", "caTrustFile"} {
			if v := d[k]; v != "" {
				s["cont3xt"][k] = v
			}
		}
		if b := c.Spec.Cont3xt.Database; b != nil {
			s["cont3xt"]["elasticsearch"] = strings.Join(b.Endpoints, ";")
			if b.TLS.CASecretRef != nil {
				s["cont3xt"]["caTrustFile"] = "/var/run/arkime-ca/bundle/ca.crt"
			}
		}
		if c.Spec.Web.Host != "" {
			s["cont3xt"]["webBasePath"] = "/cont3xt/"
		}
		s["arkime:local"] = map[string]string{"elasticsearch": d["elasticsearch"], "prefix": Prefix(c), "arkimeUrl": ViewerURL(c)}
	}
	if component == "wise" {
		s["wiseService"] = clone(s["wiseService"])
		s["wiseService"]["port"] = "8081"
		s["wiseService"]["wiseHost"] = "0.0.0.0"
	}
	enrichmentSections(c, component, s)
	return s
}
func clone(m map[string]string) map[string]string {
	r := map[string]string{}
	for k, v := range m {
		r[k] = v
	}
	return r
}

func mergePlugins(required string, extra []string) string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range append(strings.Split(required, ";"), extra...) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, ";")
}

func anyCA(c *api.ArkimeCluster) bool {
	for _, b := range Backends(c) {
		if b.TLS.CASecretRef != nil {
			return true
		}
	}
	return false
}

func DatabaseIdentity(c *api.ArkimeCluster) string {
	parts := []string{c.Spec.Database.Engine, Prefix(c)}
	endpoints := append([]string{}, c.Spec.Database.Endpoints...)
	sort.Strings(endpoints)
	parts = append(parts, endpoints...)
	if u := c.Spec.Database.Users; u != nil {
		parts = append(parts, "users", u.Engine, u.IndexPrefix)
		eps := append([]string{}, u.Endpoints...)
		sort.Strings(eps)
		parts = append(parts, eps...)
	}
	return Hash(strings.Join(parts, "\x00"))
}
