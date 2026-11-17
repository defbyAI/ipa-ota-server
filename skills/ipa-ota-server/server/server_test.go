package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestManifestUsesHostOnEveryRequest(t *testing.T) {
	source := fixtureIPA(t, "9")
	icon := fixturePNG(t)
	directory := t.TempDir()
	release, err := publish(source, directory, icon, icon)
	if err != nil {
		t.Fatal(err)
	}
	origin := startOrigin(t, directory)

	page := fetch(t, origin.Client(), http.MethodGet, origin.URL+"/", "", nil)
	if page.status != http.StatusOK || !bytes.Contains(page.body, []byte("Fixture &lt;App&gt; &amp; iPad")) {
		t.Fatalf("unexpected install page: %d %s", page.status, page.body)
	}
	manifestPath := pageManifestPath(t, page.body)
	if manifestPath != release.ManifestPath {
		t.Fatalf("page references %q, expected %q", manifestPath, release.ManifestPath)
	}
	for _, host := range []string{"downloads.example.test:8443", "vpn.example.test", "[::1]:8443"} {
		t.Run(host, func(t *testing.T) {
			headers := map[string]string{"X-Forwarded-Host": "unused.example.test"}
			reply := fetch(t, origin.Client(), http.MethodGet, origin.URL+"/"+manifestPath, host, headers)
			if reply.status != http.StatusOK || reply.headers.Get("Content-Type") != "text/xml; charset=utf-8" {
				t.Fatalf("unexpected manifest response: %d %s", reply.status, reply.body)
			}
			item := decodeManifest(t, reply.body)
			if item.Metadata["bundle-identifier"] != "test.example.fixture" || item.Metadata["bundle-version"] != "1.2.3" {
				t.Fatalf("incorrect app metadata: %+v", item.Metadata)
			}
			base := "https://" + host + "/" + strings.TrimSuffix(manifestPath, manifestName)
			expected := []manifestAsset{
				{Kind: "software-package", URL: base + packageName},
				{Kind: "display-image", URL: base + smallIconName},
				{Kind: "full-size-image", URL: base + largeIconName},
			}
			if !slices.Equal(item.Assets, expected) {
				t.Fatalf("assets %v, expected %v", item.Assets, expected)
			}
			head := fetch(t, origin.Client(), http.MethodHead, origin.URL+"/"+manifestPath, host, headers)
			if head.status != http.StatusOK || len(head.body) != 0 || head.headers.Get("Content-Length") != fmt.Sprint(len(reply.body)) {
				t.Fatalf("unexpected manifest HEAD response: %+v", head)
			}
		})
	}
}

func TestHTTPSProxyServesPublishedIPA(t *testing.T) {
	source := fixtureIPA(t, "9")
	contents := readFile(t, source)
	directory := t.TempDir()
	release, err := publish(source, directory, "", "")
	if err != nil {
		t.Fatal(err)
	}
	origin := startOrigin(t, directory)
	upstream, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(upstream))
	t.Cleanup(proxy.Close)

	page := fetch(t, proxy.Client(), http.MethodGet, proxy.URL+"/", "", nil)
	if page.status != http.StatusOK || !bytes.Contains(page.body, []byte("Build 9")) {
		t.Fatalf("unexpected page through TLS proxy: %+v", page)
	}
	manifest := fetch(t, proxy.Client(), http.MethodGet, proxy.URL+"/"+pageManifestPath(t, page.body), "", nil)
	if manifest.status != http.StatusOK {
		t.Fatalf("manifest status %d", manifest.status)
	}
	assets := decodeManifest(t, manifest.body).Assets
	if len(assets) != 1 || assets[0].URL != proxy.URL+"/"+release.IPAPath {
		t.Fatalf("manifest must point to the HTTPS endpoint: %+v", assets)
	}
	ipaURL := assets[0].URL
	full := fetch(t, proxy.Client(), http.MethodGet, ipaURL, "", nil)
	digest := sha256.Sum256(full.body)
	if full.status != http.StatusOK || hex.EncodeToString(digest[:]) != release.SHA256 {
		t.Fatal("downloaded IPA differs from the published IPA")
	}
	head := fetch(t, proxy.Client(), http.MethodHead, ipaURL, "", map[string]string{"Range": "bytes=0-31"})
	if head.status != http.StatusOK || head.headers.Get("Content-Type") != "application/octet-stream" ||
		head.headers.Get("Content-Length") != fmt.Sprint(len(contents)) || len(head.body) != 0 {
		t.Fatalf("unexpected IPA HEAD response: %+v", head)
	}
	for _, sample := range []struct {
		header string
		start  int
		end    int
	}{
		{"bytes=0-31", 0, 32}, {"bytes=-3", len(contents) - 3, len(contents)},
	} {
		part := fetch(t, proxy.Client(), http.MethodGet, ipaURL, "", map[string]string{"Range": sample.header})
		expectedRange := fmt.Sprintf("bytes %d-%d/%d", sample.start, sample.end-1, len(contents))
		if part.status != http.StatusPartialContent || part.headers.Get("Content-Range") != expectedRange ||
			!bytes.Equal(part.body, contents[sample.start:sample.end]) {
			t.Fatalf("incorrect range %q: %+v", sample.header, part)
		}
	}
	missing := fetch(t, proxy.Client(), http.MethodGet, ipaURL, "", map[string]string{"Range": "bytes=999999-"})
	if missing.status != http.StatusRequestedRangeNotSatisfiable || missing.headers.Get("Content-Range") != fmt.Sprintf("bytes */%d", len(contents)) {
		t.Fatalf("unexpected unsatisfiable range response: %+v", missing)
	}
}

func TestPublishingKeepsPreviousAssetsAndFailedPublishKeepsPage(t *testing.T) {
	directory := t.TempDir()
	source := fixtureIPA(t, "9")
	first, err := publish(source, directory, "", "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := publish(source, directory, "", "")
	if err != nil || again != first {
		t.Fatalf("identical publish changed release: %+v, %v", again, err)
	}
	last, err := publish(fixtureIPA(t, "10"), directory, "", "")
	if err != nil || first.IPAPath == last.IPAPath {
		t.Fatalf("new build must have a new release: %+v, %v", last, err)
	}
	if !bytes.Equal(readFile(t, filepath.Join(directory, first.IPAPath)), readFile(t, source)) {
		t.Fatal("older IPA changed")
	}
	origin := startOrigin(t, directory)
	oldManifest := fetch(t, origin.Client(), http.MethodGet, origin.URL+"/"+first.ManifestPath, "", nil)
	if oldManifest.status != http.StatusOK {
		t.Fatalf("older installation link returned %d", oldManifest.status)
	}
	index := filepath.Join(directory, "index.html")
	page := readFile(t, index)
	if pageManifestPath(t, page) != last.ManifestPath {
		t.Fatal("index does not refer to the new release")
	}
	badIPA := filepath.Join(t.TempDir(), "broken.ipa")
	if err := os.WriteFile(badIPA, []byte("not an IPA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := publish(badIPA, directory, "", ""); err == nil {
		t.Fatal("invalid archive was published")
	}
	if !bytes.Equal(readFile(t, index), page) {
		t.Fatal("failed publish changed the landing page")
	}
}

func TestRejectsInvalidManifestHostsAndPrivateFiles(t *testing.T) {
	directory := t.TempDir()
	release, err := publish(fixtureIPA(t, "9"), directory, "", "")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	handler := otaHandler{root: root}
	for _, host := range []string{"", "user@example.test", "example.test/path", "example.test:70000", "example.test:", "bad_host", "[example.test]"} {
		request := httptest.NewRequest(http.MethodGet, "/"+release.ManifestPath, nil)
		request.Host = host
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("invalid host %q got status %d", host, recorder.Code)
		}
	}
	for _, route := range []string{"/releases/", "/serve.py", "/../.env", "/" + strings.TrimSuffix(release.ManifestPath, manifestName) + releaseInfoName} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, route, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("private route %q got status %d", route, recorder.Code)
		}
	}
	iconPath := strings.TrimSuffix(release.IPAPath, packageName) + smallIconName
	if err := os.Symlink(fixturePNG(t), filepath.Join(directory, iconPath)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/"+iconPath, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("source outside publication root returned %d", recorder.Code)
	}
}

type reply struct {
	status  int
	headers http.Header
	body    []byte
}

func fetch(t *testing.T, client *http.Client, method, address, host string, headers map[string]string) reply {
	t.Helper()
	request, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{response.StatusCode, response.Header, body}
}

func startOrigin(t *testing.T, directory string) *httptest.Server {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	server := httptest.NewServer(otaHandler{root: root})
	t.Cleanup(server.Close)
	return server
}

func readFile(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func pageManifestPath(t *testing.T, page []byte) string {
	t.Helper()
	match := regexp.MustCompile(`data-manifest="([^"]+)"`).FindSubmatch(page)
	if match == nil {
		t.Fatal("landing page does not reference the manifest")
	}
	return string(match[1])
}

func decodeManifest(t *testing.T, body []byte) manifestItem {
	t.Helper()
	var manifest otaManifest
	if _, err := plist.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Items) != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	return manifest.Items[0]
}

func fixtureIPA(t *testing.T, build string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "fixture.ipa")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	for _, item := range []struct {
		name string
		info map[string]string
	}{
		{"Payload/Fixture.app/Info.plist", map[string]string{
			"CFBundleIdentifier":         "test.example.fixture",
			"CFBundleVersion":            build,
			"CFBundleShortVersionString": "1.2.3",
			"CFBundleDisplayName":        "Fixture <App> & iPad",
		}},
		{"Payload/Fixture.app/PlugIns/Share.appex/Info.plist", map[string]string{
			"CFBundleIdentifier": "test.example.extension",
			"CFBundleVersion":    "99",
		}},
	} {
		data, err := plist.Marshal(item.info, plist.BinaryFormat)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := archive.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}

func fixturePNG(t *testing.T) string {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}
