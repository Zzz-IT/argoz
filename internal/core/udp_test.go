package core

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func TestDatagramRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeDatagram(&buf, []byte("hello-udp")); err != nil {
		t.Fatal(err)
	}
	got, err := readDatagram(&buf, make([]byte, 64))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello-udp" {
		t.Fatalf("got %q", got)
	}
}

func TestDatagramRejectsOversize(t *testing.T) {
	var buf bytes.Buffer
	var h [2]byte
	binary.BigEndian.PutUint16(h[:], uint16(maxUDPPacket+1))
	buf.Write(h[:])
	if _, err := readDatagram(&buf, make([]byte, udpBufSize)); err == nil {
		t.Fatal("oversize datagram should fail")
	}
}

func startUDPEcho(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], addr)
		}
	}()
	return conn.LocalAddr().String()
}

func vlessUDPHeader(uuid, target string) []byte {
	h, p := hostPort(target)
	u, _ := parseUUID(uuid)
	buf := []byte{0x00}
	buf = append(buf, u[:]...)
	buf = append(buf, 0x00) // addons
	buf = append(buf, 0x02) // cmd = UDP
	buf = append(buf, byte(p>>8), byte(p))
	buf = append(buf, 0x01) // IPv4
	buf = append(buf, net.ParseIP(h).To4()...)
	return buf
}

func TestVLESSUDPEndToEnd(t *testing.T) {
	echo := startUDPEcho(t)
	uuid := newUUID()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings()
	settings.UDPEnabled = true
	settings.MaxUDPSessions = 4
	rt := &nodeRuntime{
		node:     Node{ID: "udp1", Name: "udp", Protocol: "vless", Path: "/udp", UUID: uuid},
		settings: settings,
		state:    "up",
	}
	rt.startServer(ln)
	t.Cleanup(rt.stopServer)

	conn, br := wsDial(t, ln.Addr().String(), "/udp")
	defer conn.Close()

	var packed [2]byte
	binary.BigEndian.PutUint16(packed[:], uint16(len("udp-hello")))
	payload := append(vlessUDPHeader(uuid, echo), packed[:]...)
	payload = append(payload, []byte("udp-hello")...)
	wsWrite(t, conn, opBinary, payload)

	_, resp := wsRead(t, br)
	if !bytes.Equal(resp, []byte{0x00, 0x00}) {
		t.Fatalf("VLESS response: %x", resp)
	}
	_, data := wsRead(t, br)
	if len(data) < 2 || string(data[2:]) != "udp-hello" {
		t.Fatalf("UDP response: %x", data)
	}
	if int(binary.BigEndian.Uint16(data[:2])) != len("udp-hello") {
		t.Fatalf("UDP length: %x", data[:2])
	}
}
