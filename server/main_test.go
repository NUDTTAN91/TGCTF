// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package main

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSetupGinMode 验证运行模式的选择逻辑：
// 未设置时默认 release，合法值按需生效，非法值回退到 release 而不 panic。
func TestSetupGinMode(t *testing.T) {
	originalEnv, envExists := os.LookupEnv("GIN_MODE")
	originalMode := gin.Mode()
	t.Cleanup(func() {
		if envExists {
			os.Setenv("GIN_MODE", originalEnv)
		} else {
			os.Unsetenv("GIN_MODE")
		}
		gin.SetMode(originalMode)
	})

	cases := []struct {
		env  string
		want string
	}{
		{"", gin.ReleaseMode},
		{gin.DebugMode, gin.DebugMode},
		{gin.ReleaseMode, gin.ReleaseMode},
		{gin.TestMode, gin.TestMode},
		{"bogus", gin.ReleaseMode},
	}

	for _, tc := range cases {
		os.Setenv("GIN_MODE", tc.env)
		setupGinMode()
		if got := gin.Mode(); got != tc.want {
			t.Errorf("GIN_MODE=%q: got mode %q, want %q", tc.env, got, tc.want)
		}
	}
}
