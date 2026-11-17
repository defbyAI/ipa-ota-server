package main

import (
	"archive/zip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"howett.net/plist"
)

const (
	packageName     = "app.ipa"
	smallIconName   = "icon-small.png"
	largeIconName   = "icon-large.png"
	manifestName    = "manifest.plist"
	releaseInfoName = "release.json"
)

//go:embed install.html
var pageSource string

var installPage = template.Must(template.New("install").Parse(pageSource))
var topLevelAppInfo = regexp.MustCompile(`^Payload/([^/]+)\.app/Info\.plist$`)

type appInfo struct {
	Identifier string
	Version    string
	Build      string
	Title      string
	SmallIcon  bool
	LargeIcon  bool
}

type publication struct {
	BundleIdentifier string `json:"bundle_identifier"`
	Version          string `json:"version"`
	Build            string `json:"build"`
	SHA256           string `json:"sha256"`
	Directory        string `json:"directory"`
	IPAPath          string `json:"ipa_path"`
	ManifestPath     string `json:"manifest_path"`
}

func publish(ipa, directory, smallIcon, largeIcon string) (publication, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return publication{}, err
	}
	releases := filepath.Join(directory, "releases")
	if err := os.MkdirAll(releases, 0o700); err != nil {
		return publication{}, err
	}
	if state, err := os.Lstat(releases); err != nil || !state.IsDir() {
		return publication{}, errors.New("expected a regular releases directory")
	}
	scratch, err := os.MkdirTemp(directory, ".publish-")
	if err != nil {
		return publication{}, err
	}
	defer os.RemoveAll(scratch)
	staged := filepath.Join(scratch, "release")
	if err := os.Mkdir(staged, 0o700); err != nil {
		return publication{}, err
	}

	appFile := filepath.Join(staged, packageName)
	ipaHash, err := copyAsset(ipa, appFile)
	if err != nil {
		return publication{}, fmt.Errorf("copy IPA: %w", err)
	}
	info, err := readAppInfo(appFile)
	if err != nil {
		return publication{}, fmt.Errorf("read IPA: %w", err)
	}
	info.SmallIcon = smallIcon != ""
	info.LargeIcon = largeIcon != ""
	hashes := map[string]string{packageName: ipaHash}
	for _, icon := range []struct{ source, name string }{
		{smallIcon, smallIconName}, {largeIcon, largeIconName},
	} {
		if icon.source == "" {
			continue
		}
		destination := filepath.Join(staged, icon.name)
		hashes[icon.name], err = copyAsset(icon.source, destination)
		if err != nil {
			return publication{}, err
		}
		if err := checkPNG(destination); err != nil {
			return publication{}, fmt.Errorf("read %s: %w", icon.name, err)
		}
	}
	data, err := json.Marshal(info)
	if err != nil {
		return publication{}, err
	}
	if err := os.WriteFile(filepath.Join(staged, releaseInfoName), data, 0o600); err != nil {
		return publication{}, err
	}
	infoHash := sha256.Sum256(data)
	hashes[releaseInfoName] = hex.EncodeToString(infoHash[:])
	identity, err := json.Marshal(hashes)
	if err != nil {
		return publication{}, err
	}
	releaseHash := sha256.Sum256(identity)
	releaseID := hex.EncodeToString(releaseHash[:])
	relative := "releases/" + releaseID + "/"
	iconName := ""
	if info.LargeIcon {
		iconName = largeIconName
	} else if info.SmallIcon {
		iconName = smallIconName
	}
	page := filepath.Join(scratch, "index.html")
	if err := writePage(page, info, relative, iconName); err != nil {
		return publication{}, err
	}
	destination := filepath.Join(releases, releaseID)
	if err := os.Rename(staged, destination); err != nil {
		// Publishing the same package again can reuse an intact release.
		if state, statErr := os.Lstat(destination); statErr != nil || !state.IsDir() {
			return publication{}, err
		}
		for name, expected := range hashes {
			if !sameAsset(filepath.Join(destination, name), expected) {
				return publication{}, fmt.Errorf("existing release has a different %s", name)
			}
		}
	}
	if err := os.Rename(page, filepath.Join(directory, "index.html")); err != nil {
		return publication{}, err
	}
	return publication{
		BundleIdentifier: info.Identifier,
		Version:          info.Version,
		Build:            info.Build,
		SHA256:           ipaHash,
		Directory:        directory,
		IPAPath:          relative + packageName,
		ManifestPath:     relative + manifestName,
	}, nil
}

func writePage(filename string, app appInfo, release, icon string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	err = installPage.Execute(file, struct {
		App     appInfo
		Release string
		Icon    string
	}{app, release, icon})
	return errors.Join(err, file.Close())
}

func readAppInfo(ipa string) (appInfo, error) {
	archive, err := zip.OpenReader(ipa)
	if err != nil {
		return appInfo{}, err
	}
	defer archive.Close()

	var entry *zip.File
	var appName string
	for _, file := range archive.File {
		match := topLevelAppInfo.FindStringSubmatch(file.Name)
		if match == nil {
			continue
		}
		if entry != nil {
			return appInfo{}, errors.New("found multiple top-level app Info.plists")
		}
		entry, appName = file, match[1]
	}
	if entry == nil {
		return appInfo{}, errors.New("expected a top-level app Info.plist in Payload")
	}
	const maxPlistSize = 1024 * 1024
	if entry.UncompressedSize64 > maxPlistSize {
		return appInfo{}, errors.New("app Info.plist exceeds 1 MiB")
	}
	reader, err := entry.Open()
	if err != nil {
		return appInfo{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxPlistSize+1))
	if err != nil {
		return appInfo{}, err
	}
	if len(data) > maxPlistSize {
		return appInfo{}, errors.New("app Info.plist exceeds 1 MiB")
	}

	var info struct {
		Identifier  string `plist:"CFBundleIdentifier"`
		Version     string `plist:"CFBundleShortVersionString"`
		Build       string `plist:"CFBundleVersion"`
		DisplayName string `plist:"CFBundleDisplayName"`
		Name        string `plist:"CFBundleName"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return appInfo{}, err
	}
	if strings.TrimSpace(info.Identifier) == "" || strings.TrimSpace(info.Build) == "" {
		return appInfo{}, errors.New("app Info.plist must contain a bundle identifier and build")
	}
	return appInfo{
		Identifier: info.Identifier,
		Version:    firstNonempty(info.Version, info.Build),
		Build:      info.Build,
		Title:      firstNonempty(info.DisplayName, info.Name, appName),
	}, nil
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func copyAsset(source, destination string) (string, error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("expected a regular file at %s", source)
	}
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.Create(destination)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(output, hash), input)
	if err := errors.Join(err, output.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sameAsset(filename, expected string) bool {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	return err == nil && hex.EncodeToString(hash.Sum(nil)) == expected
}

func checkPNG(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = png.DecodeConfig(file)
	return err
}
