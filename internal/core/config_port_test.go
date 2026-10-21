package core

import "testing"

func TestUpdatePortConflictRejected(t *testing.T) {
	e, _ := newTestEngine(t)
	a, err := e.Create(NodeSpec{Name: "a", Protocol: "vless", Mode: "temp"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Create(NodeSpec{Name: "b", Protocol: "vless", Mode: "temp"})
	if err != nil {
		t.Fatal(err)
	}

	if err := e.Update(a.ID, func(n *Node) error { n.Port = b.Port; return nil }); err == nil {
		t.Fatal("端口冲突应报错，而不是静默顺延")
	}
	got, _ := e.Node(a.ID)
	if got.Port != a.Port {
		t.Fatalf("冲突后应回滚到原端口 %d，实际 %d", a.Port, got.Port)
	}
}
