package detour

import (
	"fmt"
	"io"
	"net"
	"sync/atomic"
)

type directConn struct {
	net.Conn
	addr string
	// keep track of the total bytes read by this connection, atomic
	readBytes uint64
}

var (
	blockDetector atomic.Value
)

// SetCountry sets the ISO 3166-1 alpha-2 country code
// to load country specific detection rules
func SetCountry(country string) {
	blockDetector.Store(detectorByCountry(country))
}

func init() {
	blockDetector.Store(detectorByCountry(""))
}

func DialDirect(network string, addr string, ch chan conn) {
	go func() {
		log.Tracef("Dialing direct connection to %s", addr)
		conn, err := net.DialTimeout(network, addr, TimeoutToConnect)
		detector := blockDetector.Load().(*Detector)
		if err == nil {
			if detector.DNSPoisoned(conn) {
				conn.Close()
				log.Debugf("Dial directly to %s, dns hijacked, add to whitelist", addr)
				AddToWl(addr, false)
				return
			}
			log.Tracef("Dial directly to %s succeeded", addr)
			ch <- &directConn{Conn: conn, addr: addr, readBytes: 0}
			return
		} else if detector.TamperingSuspected(err) {
			log.Debugf("Dial directly to %s, tampering suspected: %s", addr, err)
			return
		}
		log.Debugf("Dial directly to %s failed: %s", addr, err)
	}()
}

func (dc *directConn) ConnType() connType {
	return connTypeDirect
}

func (dc *directConn) FirstRead(b []byte, ch chan ioResult) {
	dc.doRead(b, checkFirstRead, ch)
}
func (dc *directConn) FollowupRead(b []byte, ch chan ioResult) {
	dc.doRead(b, checkFollowupRead, ch)
}

type readChecker func([]byte, int, error, string) error

func checkFirstRead(b []byte, n int, err error, addr string) error {
	detector := blockDetector.Load().(*Detector)
	if err == nil {
		if !detector.FakeResponse(b) {
			return nil
		}
		log.Tracef("Read %d bytes from %s directly, response is hijacked", n, addr)
		AddToWl(addr, false)
		return fmt.Errorf("response is hijacked")
	}
	if err == io.EOF {
		log.Tracef("Read %d bytes from %s directly, EOF", n, addr)
		return err
	}
	log.Debugf("Error while read from %s directly: %s", addr, err)
	if detector.TamperingSuspected(err) {
		AddToWl(addr, false)
	}
	return err
}

func checkFollowupRead(b []byte, n int, err error, addr string) error {
	detector := blockDetector.Load().(*Detector)
	if err != nil {
		if err == io.EOF {
			log.Tracef("Read %d bytes from %s directly, EOF", n, addr)
			return err
		}
		if detector.TamperingSuspected(err) {
			log.Tracef("Seems %s still blocked, add to whitelist to try detour next time", addr)
			AddToWl(addr, false)
			return err
		}
		log.Tracef("Read from %s directly failed: %s", addr, err)
		return err
	}
	if detector.FakeResponse(b) {
		log.Tracef("%s still content hijacked, add to whitelist to try detour next time", addr)
		AddToWl(addr, false)
		return fmt.Errorf("content hijacked")
	}
	log.Tracef("Read %d bytes from %s directly (follow-up)", n, addr)
	return nil
}

func (dc *directConn) doRead(b []byte, checker readChecker, ch chan ioResult) {
	go func() {
		n, err := dc.Conn.Read(b)
		err = checker(b, n, err, dc.addr)
		if err != nil {
			b = nil
			n = 0
			log.Tracef("Close direct conn to %s", dc.addr)
			dc.Close()
		} else {
			atomic.AddUint64(&dc.readBytes, uint64(n))
		}
		ch <- ioResult{n, err, dc}
	}()
	return
}

func (dc *directConn) Write(b []byte, ch chan ioResult) {
	go func() {
		n, err := dc.Conn.Write(b)
		defer func() { ch <- ioResult{n, err, dc} }()
	}()
	return
}

func (dc *directConn) Close() {
	dc.Conn.Close()
	if atomic.LoadUint64(&dc.readBytes) > 0 && !wlTemporarily(dc.addr) {
		log.Tracef("no error found till closing, notify caller that %s can be dialed directly", dc.addr)
		// just fire it, but not blocking if the chan is nil or no reader
		select {
		case DirectAddrCh <- dc.addr:
		default:
		}
	}
}