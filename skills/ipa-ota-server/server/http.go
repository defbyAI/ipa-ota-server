package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"howett.net/plist"
)

var releaseAsset = regexp.MustCompile(`^/releases/[0-9a-f]{64}/[^/]+$`)
var hostLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

type otaHandler struct {
	root *os.Root
}

func (h otaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		h.serveFile(w, r, "index.html", "text/html; charset=utf-8")
		return
	}
	if !releaseAsset.MatchString(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	switch name := path.Base(r.URL.Path); name {
	case manifestName:
		h.serveManifest(w, r, path.Dir(r.URL.Path)+"/")
	case packageName:
		h.serveFile(w, r, strings.TrimPrefix(r.URL.Path, "/"), "application/octet-stream")
	case smallIconName, largeIconName:
		h.serveFile(w, r, strings.TrimPrefix(r.URL.Path, "/"), "image/png")
	default:
		http.NotFound(w, r)
	}
}

func (h otaHandler) serveFile(w http.ResponseWriter, r *http.Request, relative, contentType string) {
	file, err := h.root.Open(relative)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	if r.Method == http.MethodHead && r.Header.Get("Range") != "" {
		r = r.Clone(r.Context())
		r.Header.Del("Range")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (h otaHandler) serveManifest(w http.ResponseWriter, r *http.Request, releasePath string) {
	base, ok := httpsHost(r.Host)
	if !ok {
		http.Error(w, "Invalid Host", http.StatusBadRequest)
		return
	}
	file, err := h.root.Open(strings.TrimPrefix(releasePath, "/") + releaseInfoName)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	var info appInfo
	if err := json.NewDecoder(file).Decode(&info); err != nil {
		http.Error(w, "Invalid release metadata", http.StatusInternalServerError)
		return
	}
	body, err := makeManifest(info, base+releasePath)
	if err != nil {
		http.Error(w, "Could not build manifest", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

func httpsHost(host string) (string, bool) {
	base := "https://" + host
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host != host || strings.HasSuffix(host, ":") {
		return "", false
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", false
		}
	}
	name := parsed.Hostname()
	if strings.HasPrefix(host, "[") {
		if net.ParseIP(name) == nil || !strings.Contains(name, ":") {
			return "", false
		}
		return base, true
	}
	if len(name) > 253 {
		return "", false
	}
	for _, label := range strings.Split(name, ".") {
		if !hostLabel.MatchString(label) {
			return "", false
		}
	}
	return base, true
}

type otaManifest struct {
	Items []manifestItem `plist:"items"`
}

type manifestItem struct {
	Assets   []manifestAsset   `plist:"assets"`
	Metadata map[string]string `plist:"metadata"`
}

type manifestAsset struct {
	Kind string `plist:"kind"`
	URL  string `plist:"url"`
}

func makeManifest(info appInfo, releaseURL string) ([]byte, error) {
	assets := []manifestAsset{{Kind: "software-package", URL: releaseURL + packageName}}
	if info.SmallIcon {
		assets = append(assets, manifestAsset{Kind: "display-image", URL: releaseURL + smallIconName})
	}
	if info.LargeIcon {
		assets = append(assets, manifestAsset{Kind: "full-size-image", URL: releaseURL + largeIconName})
	}
	item := manifestItem{
		Assets: assets,
		Metadata: map[string]string{
			"bundle-identifier":   info.Identifier,
			"bundle-version":      info.Version,
			"kind":                "software",
			"platform-identifier": "com.apple.platform.iphoneos",
			"title":               info.Title,
		},
	}
	return plist.Marshal(otaManifest{Items: []manifestItem{item}}, plist.XMLFormat)
}
