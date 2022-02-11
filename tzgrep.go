package tzgrep

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

func New(expr string) (*TZgrep, error) {
	exp, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	return &TZgrep{
		Out: make(chan Result),
		exp: exp,
	}, nil
}

type TZgrep struct {
	Out chan Result
	exp *regexp.Regexp
}

type Result struct {
	Path []string
	Err  error
}

func (tz *TZgrep) Start(paths []string) {
	wg := sync.WaitGroup{}
	wg.Add(len(paths))
	go func() {
		wg.Wait()
		close(tz.Out)
	}()
	for _, p := range paths {
		p := p
		go func() {
			tz.findPath(p)
			wg.Done()
		}()
	}
}

func (tz *TZgrep) findPath(path string) {
	f, err := os.Open(path)
	if err != nil {
		tz.Out <- Result{Path: []string{path}, Err: err}
	}
	defer f.Close()
	tz.find(f, []string{path})
}

func (tz *TZgrep) find(zr io.Reader, path []string) {
	if tz.exp.MatchString(path[len(path)-1]) {
		tz.Out <- Result{Path: path}
	}
	zf, isTar := newDecompressor(path[len(path)-1])
	if !isTar {
		return
	}
	r, err := zf(zr)
	if err != nil {
		tz.Out <- Result{Path: path, Err: err}
	}
	defer r.Close()
	tr := tar.NewReader(r)
	for h, err := tr.Next(); err != io.EOF; h, err = tr.Next() {
		if err != nil {
			tz.Out <- Result{Path: path, Err: err}
			break
		}
		tz.find(tr, append(path[:len(path):len(path)], h.Name))
	}
}

type decompressor func(io.Reader) (io.ReadCloser, error)

func newDecompressor(path string) (zf decompressor, ok bool) {
	p := strings.ToLower(path)
	switch {
	case hasSuffixes(p, ".tar"):
		return func(r io.Reader) (io.ReadCloser, error) {
			return io.NopCloser(r), nil
		}, true
	case hasSuffixes(p, ".tar.gz", ".tgz", ".taz"):
		return func(r io.Reader) (io.ReadCloser, error) {
			r, err := gzip.NewReader(r)
			return io.NopCloser(r), err
		}, true
	case hasSuffixes(p, ".tar.bz2", ".tar.bz", ".tbz", ".tbz2", ".tz2", ".tb2"):
		return func(r io.Reader) (io.ReadCloser, error) {
			return io.NopCloser(bzip2.NewReader(r)), nil
		}, true
	case hasSuffixes(p, ".tar.xz", ".txz"):
		return xzReader, true
	case hasSuffixes(p, ".tar.zst", ".tzst", ".tar.zstd"):
		return zstdReader, true
	default:
		return nil, false
	}
}

func hasSuffixes(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func xzReader(r io.Reader) (io.ReadCloser, error) {
	return zCmdReader(exec.Command("xz", "-d", "-T0"), r)
}

func zstdReader(r io.Reader) (io.ReadCloser, error) {
	return zCmdReader(exec.Command("zstd", "-d"), r)
}

func zCmdReader(cmd *exec.Cmd, r io.Reader) (io.ReadCloser, error) {
	cmd.Stdin = r
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return splitCloser{out, closerFunc(func() error {
		return cmd.Wait()
	})}, nil
}

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}

type splitCloser struct {
	io.Reader
	io.Closer
}