//go:build windows

package cmd

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// registerNativeHostManifest registers the manifest path in HKCU so Chromium
// browsers can discover the native messaging host on Windows.
func registerNativeHostManifest(registryKey, manifestPath string) error {
	if registryKey == "" {
		return nil
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, registryKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create registry key %s: %w", registryKey, err)
	}
	defer key.Close()
	if err := key.SetStringValue("", manifestPath); err != nil {
		return fmt.Errorf("set registry value %s: %w", registryKey, err)
	}
	return nil
}

func nativeHostRegistered(registryKey, manifestPath string) bool {
	if registryKey == "" {
		return false
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, registryKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	val, _, err := key.GetStringValue("")
	if err != nil {
		return false
	}
	return val == manifestPath
}
