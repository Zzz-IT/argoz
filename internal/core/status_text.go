package core

import (
	"fmt"
	"time"
)

// retryHint 生成「多久后重试」的提示文本
func RetryHint(st NodeStatus) string {
	if st.NextRetry.IsZero() {
		return ""
	}
	d := time.Until(st.NextRetry)
	if d <= 0 {
		return "  [即将重试]"
	}
	return fmt.Sprintf("  [%s 后重试]", humanDuration(d))
}
