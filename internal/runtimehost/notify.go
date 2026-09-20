package runtimehost

import (
	"net"
	"os"
	"strings"
	"time"
)

// Notify is called by the actual safety loop only after it has progressed.
// No unrelated ticker can disguise a blocked recovery loop to systemd.
func Notify(message string) error {
	address := os.Getenv("NOTIFY_SOCKET")
	if address == "" {
		return nil
	}
	if strings.HasPrefix(address, "@") {
		address = "\x00" + address[1:]
	}
	conn, err := net.DialTimeout("unixgram", address, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = conn.Write([]byte(message))
	return err
}
