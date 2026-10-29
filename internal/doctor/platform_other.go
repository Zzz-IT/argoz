//go:build !linux

package doctor

import (
	"fmt"
	"os"
	"runtime"
)

func diskAvailableMB(string) (int, error) {
	return 0, fmt.Errorf("当前平台 %s 不支持 statfs", runtime.GOOS)
}

func probeDirectoryWritable(path string) (bool, string) {
	st, err := os.Stat(path)
	if err != nil {
		return false, err.Error()
	}
	if runtime.GOOS == "windows" {
		// Windows 的权限位不能可靠表示 ACL；不做写探针，避免只读自检修改目录。
		return true, "目录存在；Windows ACL 由访问结果决定"
	}
	if st.Mode().Perm()&0o222 == 0 {
		return false, fmt.Sprintf("目录权限位无任何写位: %s", st.Mode().Perm())
	}
	return true, fmt.Sprintf("目录权限位允许写入: %s", st.Mode().Perm())
}
