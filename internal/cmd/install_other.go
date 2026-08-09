//go:build !windows

package cmd

func registerNativeHostManifest(registryKey, manifestPath string) error {
	return nil
}

func nativeHostRegistered(registryKey, manifestPath string) bool {
	return false
}
