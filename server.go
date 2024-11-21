package main

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/anthdm/foreverstore/p2p"
)

type FileServerOpts struct {
	ID                string
	EncKey            []byte
	StorageRoot       string
	PathTransformFunc PathTrasformFunc
	Transport         p2p.Transport
	BootstrapNodes    []string
	//ListenAddr        string
}

type FileServer struct {
	FileServerOpts

	peerLock sync.Mutex
	peers    map[string]p2p.Peer
	store    *Store
	quitch   chan struct{}
}

func NewFileServer(opts FileServerOpts) *FileServer {
	storeOpts := StoreOpts{
		Root:             opts.StorageRoot,
		PathTrasformFunc: opts.PathTransformFunc,
	}

	if len(opts.ID) == 0 {
		opts.ID = generateID()
	}

	return &FileServer{
		FileServerOpts: opts,
		store:          NewStore(storeOpts),
		quitch:         make(chan struct{}),
		peers:          make(map[string]p2p.Peer),
	}
}

func (s *FileServer) broadcast(msg *Message) error {

	buf := new(bytes.Buffer)
	if err := gob.NewEncoder(buf).Encode(msg); err != nil {
		return err
	}

	for _, peer := range s.peers {
		peer.Send([]byte{p2p.IncomingMessage})
		if err := peer.Send(buf.Bytes()); err != nil {
			return err
		}
	}

	return nil
}

type Message struct {
	Payload any
}

type MessageStoreFile struct {
	ID   string
	Key  string
	Size int64
}

type MessageGetFile struct {
	ID  string
	Key string
}

func (s *FileServer) Get(key string) (io.Reader, error) {
	if s.store.Has(s.ID, key) {
		fmt.Printf("[%s] serving file (%s) from local disk\n", s.Transport.Addr(), key)
		_, r, err := s.store.Read(s.ID, key)
		return r, err
	}

	fmt.Printf("[%s] dont have file (%s) locally, fetching from network...\n", s.Transport.Addr(), key)

	msg := Message{
		Payload: MessageGetFile{
			ID:  s.ID,
			Key: hashKey(key),
		},
	}

	if err := s.broadcast(&msg); err != nil {
		return nil, err
	}

	time.Sleep(time.Millisecond * 500)

	for _, peer := range s.peers {
		// First read the line size so we can limit the amount of bytes that we read
		// from the connection, so it will not keep hanging
		var fileSize int64
		binary.Read(peer, binary.LittleEndian, &fileSize)
		n, err := s.store.writeDeccrypt(s.EncKey, s.ID, key, io.LimitReader(peer, fileSize))
		if err != nil {
			return nil, err
		}

		fmt.Printf("[%s] received (%d) bytes over the network from (%s) ", s.Transport.Addr(), n, peer.RemoteAddr())

		peer.CloseStream()

		// peer.Send([]byte[p2p.IncomingMessage, fileBuffer, peer])

		//fmt.Println("receiving stream from peer: ", peer.RemoteAddr())
		// fileBuffer := new(bytes.Buffer)
		// n, err := io.CopyN(fileBuffer, peer, 22)
		// if err != nil {
		// 	return nil, err
		// }

		//fmt.Println(fileBuffer.String())
	}

	_, r, err := s.store.Read(s.ID, key)
	return r, err
}

func (s *FileServer) Store(key string, r io.Reader) error {

	// 1. Store this file to disk
	// 2. broeadcast this file to all known peers in the network

	var (
		fileBuffer = new(bytes.Buffer)
		tee        = io.TeeReader(r, fileBuffer)
	)

	size, err := s.store.Write(s.ID, key, tee)
	if err != nil {
		return err
	}

	msg := Message{
		Payload: MessageStoreFile{
			ID:   s.ID,
			Key:  hashKey(key),
			Size: size + 16,
		},
	}

	if err := s.broadcast(&msg); err != nil {
		return err
	}

	time.Sleep(time.Millisecond * 5)

	peers := []io.Writer{}
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	mw := io.MultiWriter(peers...)
	mw.Write([]byte{p2p.IncomingMessage})
	n, err := copyEncrypt(s.EncKey, fileBuffer, mw)
	if err != nil {
		return err
	}

	fmt.Printf("[%s] received and written (%d) bytes to disk: \n", s.Transport.Addr(), n)

	return nil

	/*
		// TODO: (@anthdm) use a multiwriter here.
		for _, peer := range s.peers {
			peer.Send([]byte{p2p.IncomingMessage})
			n, err := io.Copy(peer, fileBuffer)
			if err != nil {
				return err
			}

			fmt.Println("received and written bytes to disk %s", n)
		}

		return nil
	*/

	/*
		buf := new(bytes.Buffer)
		tee := io.TeeReader(r, buf)

		if err := s.store.Write(key, tee); err != nil {
			return err
		}
	*/

	/*
		_, err := io.Copy(buf, r)
		if err != nil {
			return err
		}
	*/

	/*
		p := &DataMessage{
			Key:  key,
			Data: buf.Bytes(),
		}

		fmt.Println(buf.Bytes())

		return s.broadcast(&Message{
			From:    "todo",
			Payload: p,
		})
	*/
}

func (s *FileServer) Stop() {
	close(s.quitch)
}

func (s *FileServer) OnPeer(p p2p.Peer) error {
	s.peerLock.Lock()
	defer s.peerLock.Unlock()

	s.peers[p.RemoteAddr().String()] = p

	log.Printf("connected with remote %s", p.RemoteAddr())

	return nil
}

func (s *FileServer) Loop() {
	defer func() {
		log.Println("file server stopped due to error or user quit action")
		s.Transport.Close()
	}()

	for {
		select {
		case rpc := <-s.Transport.Consume():
			var msg Message
			if err := gob.NewDecoder(bytes.NewBuffer(rpc.Payload)).Decode(&msg); err != nil {
				log.Println("decoding error:", err)
			}

			if err := s.handleMessage(rpc.From, &msg); err != nil {
				log.Println("handle message error:", err)
			}

			/*
				//fmt.Printf("recv: %s\n", string(msg.Payload.([]byte)))
				fmt.Printf("payload: %+v\n", msg.Payload)

				peer, ok := s.peers[rpc.From]
				if !ok {
					panic("peer not found in peers map")
				}

				b := make([]byte, 1000)
				if _, err := peer.Read(b); err != nil {
					panic(err)
				}

				fmt.Printf("%s\n", string(b))

				peer.(*p2p.TCPPeer).Wg.Done()

				// if err := s.handleMesssage(&m); err != nil {
				//	 log.Println(err)
				// }
			*/

		case <-s.quitch:
			return
		}
	}
}

func (s *FileServer) handleMessage(from string, msg *Message) error {
	switch v := msg.Payload.(type) {
	case MessageStoreFile:
		return s.handleMessageStoreFile(from, v)
	case MessageGetFile:
		return s.handleMessageGetFile(from, v)
	}

	return nil
}

func (s *FileServer) handleMessageGetFile(from string, msg MessageGetFile) error {
	//fmt.Println("need to get a file from disk and send it over the wire")
	if !s.store.Has(msg.ID, msg.Key) {
		return fmt.Errorf("[%s] need to server file (%s) but it does not exist on disk", s.Transport.Addr(), msg.Key)
	}

	fmt.Printf("[%s] serving file (%s) over the network\n", s.Transport.Addr(), msg.Key)

	fileSize, r, err := s.store.Read(msg.ID, msg.Key)
	if err != nil {
		return err
	}

	if rc, ok := r.(io.ReadCloser); ok {
		fmt.Println("closing readCloser")
		defer rc.Close()
	}

	peer, ok := s.peers[from]
	if !ok {
		return fmt.Errorf("peer %s not in map", from)
	}

	// First send the "incomingStream" byte to the peer and then we can send
	// the file size as an int64
	peer.Send([]byte{p2p.IncomingStream})
	binary.Write(peer, binary.LittleEndian, fileSize)

	n, err := io.Copy(peer, r)
	if err != nil {
		return err
	}

	fmt.Printf("[%s] written (%d) bytes over the network to %s\n", s.Transport.Addr(), n, from)

	return nil
}

func (s *FileServer) handleMessageStoreFile(from string, msg MessageStoreFile) error {

	peer, ok := s.peers[from]
	if !ok {
		return fmt.Errorf("peer (%s) could not be found in the peer list", from)
	}

	n, err := s.store.Write(msg.ID, msg.Key, io.LimitReader(peer, msg.Size))
	if err != nil {
		return err
	}

	fmt.Printf("[%s] written %d bytes to disk\n", s.Transport.Addr(), n)

	peer.CloseStream()

	return nil

	//fmt.Printf("recv store file msg: %+v\n", msg)
}

func (s *FileServer) BootstrapNetwork() error {
	for _, addr := range s.BootstrapNodes {

		if len(addr) == 0 {
			continue
		}

		go func(addr string) {
			fmt.Printf("[%s] attemping to connect with remote %s\n", s.Transport.Addr(), addr)

			if err := s.Transport.Dial(addr); err != nil {
				log.Fatal("dial error: ", err)
			}
		}(addr)
	}

	return nil
}

func (s *FileServer) Start() error {
	fmt.Printf("[%s] starting fileserver...\n", s.Transport.Addr())

	if err := s.Transport.ListenAndAccept(); err != nil {
		return err
	}

	s.BootstrapNetwork()

	s.Loop()

	return nil
}

func init() {
	gob.Register(MessageStoreFile{})
	gob.Register(MessageGetFile{})
}

/*
func (s *FileServer) stream(msg *Message) error {
	peers := []io.Writer{}
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}

	mv := io.MultiWriter(peers...)
	return gob.NewEncoder(mv).Encode(msg)
	/*
		for _, peer := range s.peers {
			if err := gob.NewEncoder(peer).Encode(p); err != nil {
				return err
			}
		}

		return nil
*/

/*
	buf := new(bytes.Buffer)
	for _, peer := range s.peers {
		if err := gob.NewEncoder(buf).Encode(p); err != nil {
			return err
		}

		peer.Send(buf.Bytes())
	}

	return nil
*/
//}
