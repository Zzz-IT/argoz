package core

import "testing"

func TestPortBoundsSinglePort(t *testing.T) {
	s := Settings{PortRange: "8080-8080"}
	lo, hi, err := s.PortBounds()
	if err != nil {
		t.Fatalf("单个端口应被接受: %v", err)
	}
	if lo != 8080 || hi != 8080 {
		t.Fatalf("解析结果异常: %d-%d", lo, hi)
	}
}

func TestPortBoundsRejectsReversed(t *testing.T) {
	s := Settings{PortRange: "9000-8000"}
	if _, _, err := s.PortBounds(); err == nil {
		t.Fatal("起始大于结束应报错")
	}
}
