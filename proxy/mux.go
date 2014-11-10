// Copyright 2014 The Serviced Authors.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package proxy

import (
	"github.com/control-center/serviced/dao"
	"github.com/zenoss/glog"

	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TCPMux is an implementation of tcp muxing RFC 1078.
type TCPMux struct {
	listener    net.Listener    // the connection this mux listens on
	connections chan net.Conn   // stream of accepted connections
	closing     chan chan error // shutdown noticiation
	hostID      string
	hostIP      string
}

// NewTCPMux creates a new tcp mux with the given listener. If it succees, it
// is expected that this object is the owner of the listener and will close it
// when Close() is called on the TCPMux.
func NewTCPMux(listener net.Listener, hostID, hostIP string) (mux *TCPMux, err error) {
	if listener == nil {
		return nil, fmt.Errorf("listener can not be nil")
	}
	mux = &TCPMux{
		listener:    listener,
		connections: make(chan net.Conn),
		closing:     make(chan chan error),
		hostID:      hostID,
		hostIP:      hostIP,
	}
	go mux.loop()
	return mux, nil
}

func (mux *TCPMux) Close() {
	glog.V(5).Info("Close Called")
	close(mux.closing)
}

func (mux *TCPMux) acceptor(listener net.Listener, closing chan chan struct{}) {
	defer func() {
		close(mux.connections)
	}()
	for {
		conn, err := mux.listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "too many open files") {
				glog.Warningf("error accepting connections, retrying in 50 ms: %s", err)
				select {
				case <-closing:
					glog.V(5).Info("shutting down acceptor")
					return
				case <-time.After(time.Millisecond * 50):
					continue
				}
			}
			glog.Errorf("shutting down acceptor: %s", err)
			return
		}
		glog.V(5).Infof("accepted connection: %s", conn)
		select {
		case <-closing:
			glog.V(5).Info("shutting down acceptor")
			conn.Close()
			return
		case mux.connections <- conn:
		}
	}
}

func (mux *TCPMux) loop() {
	glog.V(5).Infof("entering TPCMux loop")
	closeAcceptor := make(chan chan struct{})
	go mux.acceptor(mux.listener, closeAcceptor)
	for {
		select {
		case errc := <-mux.closing:
			glog.V(5).Info("Closing mux")
			closeAcceptorAck := make(chan struct{})
			mux.listener.Close()
			closeAcceptor <- closeAcceptorAck
			errc <- nil
			return
		case conn, ok := <-mux.connections:
			if !ok {
				mux.connections = nil
				glog.V(6).Info("got nil conn, channel is closed")
				continue
			}
			glog.V(5).Info("handing mux connection")
			go mux.muxConnection(conn)
		}
	}
}

// updateConnectionInfo updates mux connection info
func (mux *TCPMux) updateConnectionInfo(parts []string, src, dst net.Conn) string {
	now := time.Now()
	tmci := TCPMuxConnectionInfo{
		AgentHostIP:   mux.hostIP,
		AgentHostID:   mux.hostID,
		SrcRemoteAddr: src.RemoteAddr().String(),
		SrcLocalAddr:  src.LocalAddr().String(),
		DstLocalAddr:  dst.LocalAddr().String(),
		DstRemoteAddr: dst.RemoteAddr().String(),
		DstName:       parts[0],
		ActiveWriters: 1,
		CreatedAt:     now,
	}

	if len(parts) > 2 {
		part := parts[1]
		glog.V(2).Infof("unmarshalling into MuxSource: %+v", part)
		str := strings.Replace(part, "===", ":", -1)
		err := json.Unmarshal([]byte(str), &tmci.Src)
		if err != nil {
			glog.Errorf("Could not unmarshal into MuxSource: %s", part)
		}
	}

	if len(parts) > 3 {
		part := parts[2]
		glog.V(2).Infof("unmarshalling into ApplicationEndpoint: %+v", part)
		aestr := strings.Replace(part, "===", ":", -1)
		err := json.Unmarshal([]byte(aestr), &tmci.ApplicationEndpoint)
		if err != nil {
			glog.Errorf("Could not unmarshal into ApplicationEndpoint: %s", part)
		}
	}

	muxConnectionInfoLock.Lock()
	defer muxConnectionInfoLock.Unlock()

	key := fmt.Sprintf("%-15s %-21s <-- %-15s %-21s", tmci.AgentHostIP, tmci.DstRemoteAddr, tmci.Src.AgentHostIP, tmci.SrcRemoteAddr)
	if _, ok := muxConnectionInfo[key]; ok {
		tmci.CreatedAt = muxConnectionInfo[key].CreatedAt
		tmci.ActiveWriters += muxConnectionInfo[key].ActiveWriters
	}
	muxConnectionInfo[key] = &tmci
	glog.V(2).Infof("TCPMuxConnectionInfo (count:%d): %+v", len(muxConnectionInfo), tmci)

	return key
}

// decrementActiveWriters decrements the number of writers by one
func (mux *TCPMux) decrementActiveWriters(key string) {
	muxConnectionInfoLock.Lock()
	defer muxConnectionInfoLock.Unlock()
	muxConnectionInfo[key].ActiveWriters--
	if muxConnectionInfo[key].ActiveWriters <= 0 {
		if _, ok := muxConnectionInfo[key]; ok {
			delete(muxConnectionInfo, key)
		}
	}
}

// muxConnection takes an inbound connection reads a line from it and
// then attempts to set up a connection to the service specified by the
// line. The service is specified in the form "IP:PORT\n". If the connection
// to the service is sucessful, all traffic continues to be proxied between
// two connections.
func (mux *TCPMux) muxConnection(conn net.Conn) {
	// make sure that we don't block indefinitely
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		glog.Errorf("could not read mux line: %s", err)
		conn.Close()
		return
	}
	// restore deadline
	conn.SetReadDeadline(time.Time{})
	line = strings.TrimSpace(line)
	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		glog.Errorf("malformed mux line: %s", line)
		conn.Close()
		return
	}
	address := fmt.Sprintf("%s:%s", parts[len(parts)-2], parts[len(parts)-1])

	svc, err := net.Dial("tcp4", address)
	if err != nil {
		glog.Errorf("got %s => %s, could not dial to '%s' : %s", conn.LocalAddr(), conn.RemoteAddr(), line, err)
		conn.Close()
		return
	}

	glog.V(2).Infof("mux line: %+v", line)
	key := mux.updateConnectionInfo(parts, conn, svc)

	// write any pending buffered data that wasn't part of the service spec
	if reader.Buffered() > 0 {
		bufferedBytes, err := reader.Peek(reader.Buffered())
		if err != nil {
			glog.Errorf("error peaking at buffered bytes: %s", err)
		}
		n, err := conn.Write(bufferedBytes)
		if err != nil {
			glog.Errorf("error writing buffered bytes: %s", err)
		}
		if n != len(bufferedBytes) {
			glog.Errorf("expected to write %d bytes but wrote %s", len(bufferedBytes), n)
		}
	}

	quit := make(chan bool)
	go mux.proxyLoop(conn, svc, quit, key)

	//	go func() {
	//		io.Copy(conn, svc)
	//		conn.Close()
	//		svc.Close()
	//	}()
	//	go func() {
	//		io.Copy(svc, conn)
	//		conn.Close()
	//		svc.Close()
	//	}()
}

func (mux *TCPMux) proxyLoop(client net.Conn, backend net.Conn, quit chan bool, key string) {
	//	backend, err := net.DialTCP("tcp", nil, backendAddr)
	//	if err != nil {
	//		glog.Errorf("Can't forward traffic to backend tcp/%v: %s\n", backendAddr, err)
	//		client.Close()
	//		return
	//	}

	event := make(chan int64)
	var broker = func(to, from net.Conn) {
		written, err := io.Copy(to, from)
		if err != nil {
			// If the socket we are writing to is shutdown with
			// SHUT_WR, forward it to the other end of the pipe:
			if err, ok := err.(*net.OpError); ok && err.Err == syscall.EPIPE {
				from.Close()
			}
		}
		to.Close()
		event <- written
	}

	go broker(client, backend)
	go broker(backend, client)

	var transferred int64 = 0
	for i := 0; i < 2; i++ {
		select {
		case written := <-event:
			transferred += written
		case <-quit:
			// Interrupt the two brokers and "join" them.
			mux.decrementActiveWriters(key)
			client.Close()
			backend.Close()
			for ; i < 2; i++ {
				transferred += <-event
			}
			return
		}
	}
	//	glog.Infof("transferred %v bytes between %v", transferred, backendAddr)
	mux.decrementActiveWriters(key)
	client.Close()
	backend.Close()
}

// TCPMuxConnectionInfo contains info about mux connections
type TCPMuxConnectionInfo struct {
	AgentHostIP         string
	AgentHostID         string
	Src                 MuxSource
	SrcRemoteAddr       string
	SrcLocalAddr        string
	DstLocalAddr        string
	DstRemoteAddr       string
	DstName             string
	ApplicationEndpoint dao.ApplicationEndpoint
	ActiveWriters       int
	CreatedAt           time.Time
}

// GetMuxConnectionInfo returns a map of the mux connection info
func GetMuxConnectionInfo() map[string]TCPMuxConnectionInfo {
	response := make(map[string]TCPMuxConnectionInfo)
	muxConnectionInfoLock.Lock()
	defer muxConnectionInfoLock.Unlock()
	for k, v := range muxConnectionInfo {
		response[k] = *v
	}
	return response
}

// MuxSource contains info about the source connecting to ApplicationEndpoint
type MuxSource struct {
	AgentHostIP string
	AgentHostID string
	ServiceName string
	TenantID    string
	ServiceID   string
	InstanceID  string
	ContainerIP string
	ContainerID string
}

var (
	muxConnectionInfoLock sync.RWMutex
	muxConnectionInfo     map[string]*TCPMuxConnectionInfo
)

func init() {
	muxConnectionInfo = make(map[string]*TCPMuxConnectionInfo)
}
