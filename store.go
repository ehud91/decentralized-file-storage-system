package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const defaultRootFolderName = "ggnetwork"

func CASPathTransformFunc(key string) PathKey {
	hash := sha1.Sum([]byte(key))
	hashStr := hex.EncodeToString(hash[:])

	blocksize := 5
	sliceLen := len(hashStr) / blocksize
	paths := make([]string, sliceLen)

	for i := 0; i < sliceLen; i++ {
		from, to := i*blocksize, (i*blocksize)+blocksize
		paths[i] = hashStr[from:to]
	}

	return PathKey{
		PathName: strings.Join(paths, "/"),
		Filename: hashStr,
	}
}

type PathTrasformFunc func(string) PathKey

type PathKey struct {
	PathName string
	Filename string
}

func (p PathKey) FirstPathName() string {
	paths := strings.Split(p.PathName, "/")
	if len(paths) == 0 {
		return ""
	}

	return paths[0]
}

func (p PathKey) Fullpath() string {
	return fmt.Sprintf("%s/%s", p.PathName, p.Filename)
}

type StoreOpts struct {
	// Root is the folder name of the root, containing all the folders/files of the system
	Root             string
	PathTrasformFunc PathTrasformFunc
}

var DefaultPathTransformFunc = func(key string) PathKey {
	return PathKey{
		PathName: key,
		Filename: key,
	}
}

type Store struct {
	StoreOpts
}

func NewStore(opts StoreOpts) *Store {
	if opts.PathTrasformFunc == nil {
		opts.PathTrasformFunc = DefaultPathTransformFunc
	}
	if len(opts.Root) == 0 {
		opts.Root = defaultRootFolderName
	}
	return &Store{
		StoreOpts: opts,
	}
}

func (s *Store) Has(id string, key string) bool {
	pathKey := s.PathTrasformFunc(key)
	fullpathWithRoot := fmt.Sprintf("%s/%s/%s", s.Root, id, pathKey.Fullpath())

	_, err := os.Stat(fullpathWithRoot)
	return !errors.Is(err, os.ErrNotExist)
}

func (s *Store) Clear() error {
	return os.RemoveAll(s.Root)
}

func (s *Store) Delete(id string, key string) error {
	pathKey := s.PathTrasformFunc(key)

	defer func() {
		log.Printf("deleted [%s] from disk", pathKey.Filename)
	}()

	firstPathNameWithRoot := fmt.Sprintf("%s/%s/%s", s.Root, id, pathKey.FirstPathName())

	return os.RemoveAll(firstPathNameWithRoot)
}

func (s *Store) writeDeccrypt(enckey []byte, id string, key string, r io.Reader) (int64, error) {
	f, err := s.openFIleForWriting(id, key)
	if err != nil {
		return 0, err
	}
	n, err := copyDecrypt(enckey, r, f)
	return int64(n), err
}

func (s *Store) openFIleForWriting(id string, key string) (*os.File, error) {
	pathkey := s.PathTrasformFunc(key)
	pathNameWithRoot := fmt.Sprintf("%s/%s/%s", s.Root, id, pathkey.PathName)
	if err := os.MkdirAll(pathNameWithRoot, os.ModePerm); err != nil {
		return nil, err
	}

	fullpathWithRoot := fmt.Sprintf("%s/%s/%s", s.Root, id, pathkey.Fullpath())

	return os.Create(fullpathWithRoot)
}

func (s *Store) Write(id string, key string, r io.Reader) (int64, error) {
	return s.writeStream(id, key, r)
}

func (s *Store) writeStream(id string, key string, r io.Reader) (int64, error) {

	f, err := s.openFIleForWriting(id, key)
	if err != nil {
		return 0, err
	}

	return io.Copy(f, r)

	/*
		pathkey := s.PathTrasformFunc(key)
		pathNameWithRoot := fmt.Sprintf("%s/%s/%s", s.Root, id, pathkey.PathName)
		if err := os.MkdirAll(pathNameWithRoot, os.ModePerm); err != nil {
			return 0, err
		}

		fullpathWithRoot := fmt.Sprintf("%s%s", s.Root, pathkey.Fullpath())

		f, err := os.Create(fullpathWithRoot)
		if err != nil {
			return 0, err
		}

		n, err := io.Copy(f, r)
		if err != nil {
			return 0, err
		}

		// log.Printf("written (%d) bytes to disk: %s", n, fullpathWithRoot)

		return n, nil
	*/
}

func (s *Store) Read(id string, key string) (int64, io.Reader, error) {
	return s.readStream(id, key)
	// n, f, err := s.readStream(key)
	// if err != nil {
	// 	return n, nil, err
	// }

	// defer f.Close()

	// buf := new(bytes.Buffer)
	// _, err = io.Copy(buf, f)

	// return n, buf, err
}

func (s *Store) readStream(id string, key string) (int64, io.ReadCloser, error) {
	pathKey := s.PathTrasformFunc(key)
	fullpathWithRoot := fmt.Sprintf("%s/%s/%s", s.Root, id, pathKey.Fullpath())

	file, err := os.Open(fullpathWithRoot)
	if err != nil {
		return 0, nil, err
	}

	fi, err := file.Stat()
	if err != nil {
		return 0, nil, err
	}

	return fi.Size(), file, nil

	// _, err := os.Stat(fullpathWithRoot)
	// if err != nil {
	// 	return 0, nil, err
	// }
}
