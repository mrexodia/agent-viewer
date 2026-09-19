package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type arrayFlags []string

func (a *arrayFlags) String() string         { return strings.Join(*a, ",") }
func (a *arrayFlags) Set(value string) error { *a = append(*a, value); return nil }

func expandPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func validateSource(source string) error {
	if source == "" || strings.Contains(source, "..") || strings.ContainsAny(source, "/\\:") || source == "." {
		return fmt.Errorf("invalid source label %q", source)
	}
	return nil
}

func parseWatchSpec(spec string) (WatchDir, error) {
	path, source := spec, ""
	// Do not mistake the drive letter of a plain Windows path for a source label.
	drivePath := len(spec) >= 3 && spec[1] == ':' && (spec[2] == '\\' || spec[2] == '/') &&
		((spec[0] >= 'A' && spec[0] <= 'Z') || (spec[0] >= 'a' && spec[0] <= 'z'))
	if index := strings.IndexByte(spec, ':'); index >= 0 && !drivePath {
		source, path = spec[:index], spec[index+1:]
		if err := validateSource(source); err != nil {
			return WatchDir{}, err
		}
	}
	if path == "" {
		return WatchDir{}, fmt.Errorf("watch path is empty")
	}
	path = expandPath(path)
	if source == "" {
		source = filepath.Base(normalizePath(path))
	}
	if err := validateSource(source); err != nil {
		return WatchDir{}, err
	}
	return WatchDir{Path: path, Source: source}, nil
}
