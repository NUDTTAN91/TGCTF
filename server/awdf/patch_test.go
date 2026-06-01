// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package awdf

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeZip 按 entries 生成一个测试用 ZIP，返回其路径
func writeZip(t *testing.T, name string, entries map[string][]byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建 zip 失败: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for entryName, content := range entries {
		e, err := w.Create(entryName)
		if err != nil {
			t.Fatalf("写入 zip 条目失败: %v", err)
		}
		if _, err := e.Write(content); err != nil {
			t.Fatalf("写入 zip 内容失败: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	return path
}

// TestExtractPatchZipRejectsOversizedContent 高压缩比 ZIP 不应把磁盘写满：
// 全零内容压缩比极高，几百 KB 的包可声明解出数百 MB。
func TestExtractPatchZipRejectsOversizedContent(t *testing.T) {
	oversized := bytes.Repeat([]byte{0}, maxPatchExtractSize+(1<<20))
	zipPath := writeZip(t, "bomb.zip", map[string][]byte{"bomb.bin": oversized})

	if info, err := os.Stat(zipPath); err == nil {
		t.Logf("压缩包体积 %d 字节，声明解压 %d 字节", info.Size(), len(oversized))
	}

	err := extractPatchZip(zipPath, t.TempDir())
	if err == nil {
		t.Fatal("解压超过体积上限的补丁包时应报错")
	}
	if !strings.Contains(err.Error(), "体积") {
		t.Errorf("错误信息应说明体积超限，实际为: %v", err)
	}
}

// TestExtractPatchZipRejectsPathTraversal 条目不得写到目标目录之外
func TestExtractPatchZipRejectsPathTraversal(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside_marker")
	zipPath := writeZip(t, "slip.zip", map[string][]byte{
		"../../../../" + strings.TrimPrefix(outside, "/"): []byte("escaped"),
	})

	if err := extractPatchZip(zipPath, t.TempDir()); err == nil {
		t.Fatal("解压含路径穿越条目的补丁包时应报错")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("条目逃出了目标目录，被写到 %s", outside)
	}
}

// TestExtractPatchZipRejectsTooManyFiles 文件数需受限，避免海量小文件耗尽 inode
func TestExtractPatchZipRejectsTooManyFiles(t *testing.T) {
	entries := make(map[string][]byte, maxPatchFileCount+1)
	for i := 0; i <= maxPatchFileCount; i++ {
		entries[fmt.Sprintf("f%d.txt", i)] = []byte("x")
	}
	zipPath := writeZip(t, "many.zip", entries)

	err := extractPatchZip(zipPath, t.TempDir())
	if err == nil {
		t.Fatal("解压超过文件数上限的补丁包时应报错")
	}
	if !strings.Contains(err.Error(), "文件数") {
		t.Errorf("错误信息应说明文件数超限，实际为: %v", err)
	}
}

// TestExtractPatchZipAcceptsNormalPatch 正常补丁应原样解出，含目录层级
func TestExtractPatchZipAcceptsNormalPatch(t *testing.T) {
	want := []byte("<?php echo 'patched'; ?>")
	zipPath := writeZip(t, "good.zip", map[string][]byte{
		"var/www/html/index.php": want,
	})

	destDir := t.TempDir()
	if err := extractPatchZip(zipPath, destDir); err != nil {
		t.Fatalf("正常补丁不应被拒绝: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "var/www/html/index.php"))
	if err != nil {
		t.Fatalf("解压后未找到预期文件: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("解压内容不一致，got %q want %q", got, want)
	}
}
