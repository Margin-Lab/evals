package testassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	DefaultMaxArchiveBytes  int64 = 128 << 20
	defaultMaxExtractedSize int64 = 512 << 20
)

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Descriptor struct {
	ArchiveTGZBase64 string `json:"archive_tgz_base64"`
	ArchiveTGZSHA256 string `json:"archive_tgz_sha256"`
	ArchiveTGZBytes  int    `json:"archive_tgz_bytes"`
}

func ContainsPath(desc Descriptor, relPath string, maxArchiveBytes int64) (bool, error) {
	needle, err := sanitizeArchivePath(relPath)
	if err != nil {
		return false, fmt.Errorf("normalize archive path %q: %w", relPath, err)
	}
	payload, err := DecodeAndValidate(desc, maxArchiveBytes)
	if err != nil {
		return false, err
	}
	found := false
	if _, err := walkArchive(payload, defaultMaxExtractedSize, func(name string, _ *tar.Header, _ io.Reader) error {
		if name == needle {
			found = true
		}
		return nil
	}); err != nil {
		return false, err
	}
	return found, nil
}

func PackDir(root string) (Descriptor, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Descriptor{}, fmt.Errorf("test assets root is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return Descriptor{}, fmt.Errorf("stat test assets root: %w", err)
	}
	if !info.IsDir() {
		return Descriptor{}, fmt.Errorf("test assets root must be a directory")
	}

	type fileEntry struct {
		absolute string
		relative string
		mode     fs.FileMode
	}
	entries := make([]fileEntry, 0)
	if err := filepath.WalkDir(root, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported in test assets: %s", current)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular file in test assets: %s", current)
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		rel, err = sanitizeArchivePath(rel)
		if err != nil {
			return fmt.Errorf("invalid test asset path %q: %w", rel, err)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, fileEntry{
			absolute: current,
			relative: rel,
			mode:     info.Mode().Perm(),
		})
		return nil
	}); err != nil {
		return Descriptor{}, fmt.Errorf("walk test assets: %w", err)
	}
	if len(entries) == 0 {
		return Descriptor{}, fmt.Errorf("test assets directory must contain at least one file")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].relative < entries[j].relative })

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return Descriptor{}, fmt.Errorf("create gzip writer: %w", err)
	}
	zeroTime := time.Unix(0, 0).UTC()
	gz.Header.ModTime = zeroTime
	gz.Header.Name = ""
	gz.Header.Comment = ""
	gz.Header.Extra = nil
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		body, err := os.ReadFile(entry.absolute)
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return Descriptor{}, fmt.Errorf("read test asset file %s: %w", entry.absolute, err)
		}
		header := &tar.Header{
			Name:     entry.relative,
			Mode:     int64(entry.mode.Perm()),
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
			ModTime:  zeroTime,
			Uid:      0,
			Gid:      0,
			Uname:    "",
			Gname:    "",
		}
		if err := tw.WriteHeader(header); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return Descriptor{}, fmt.Errorf("write tar header for %s: %w", entry.relative, err)
		}
		if _, err := tw.Write(body); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return Descriptor{}, fmt.Errorf("write tar file %s: %w", entry.relative, err)
		}
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return Descriptor{}, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Descriptor{}, fmt.Errorf("close gzip writer: %w", err)
	}

	payload := buf.Bytes()
	sum := sha256.Sum256(payload)
	return Descriptor{
		ArchiveTGZBase64: base64.StdEncoding.EncodeToString(payload),
		ArchiveTGZSHA256: hex.EncodeToString(sum[:]),
		ArchiveTGZBytes:  len(payload),
	}, nil
}

func ValidateDescriptor(desc Descriptor, maxArchiveBytes int64) error {
	_, err := DecodeAndValidate(desc, maxArchiveBytes)
	return err
}

func DecodeAndValidate(desc Descriptor, maxArchiveBytes int64) ([]byte, error) {
	encoded := strings.TrimSpace(desc.ArchiveTGZBase64)
	digest := strings.TrimSpace(desc.ArchiveTGZSHA256)
	if encoded == "" {
		return nil, fmt.Errorf("archive_tgz_base64 is required")
	}
	if desc.ArchiveTGZBytes <= 0 {
		return nil, fmt.Errorf("archive_tgz_bytes must be > 0")
	}
	if !sha256Pattern.MatchString(digest) {
		return nil, fmt.Errorf("archive_tgz_sha256 must be lowercase hex sha256")
	}
	limit := maxArchiveBytes
	if limit <= 0 {
		limit = DefaultMaxArchiveBytes
	}

	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode archive_tgz_base64: %w", err)
	}
	if len(payload) != desc.ArchiveTGZBytes {
		return nil, fmt.Errorf("archive_tgz_bytes mismatch: got %d decoded bytes, expected %d", len(payload), desc.ArchiveTGZBytes)
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("archive_tgz_bytes exceeds max size: %d > %d", len(payload), limit)
	}
	sum := sha256.Sum256(payload)
	actualDigest := hex.EncodeToString(sum[:])
	if actualDigest != digest {
		return nil, fmt.Errorf("archive_tgz_sha256 mismatch: got %s expected %s", actualDigest, digest)
	}
	if _, err := walkArchive(payload, defaultMaxExtractedSize, nil); err != nil {
		return nil, err
	}
	return payload, nil
}

func Materialize(desc Descriptor, destDir string, maxArchiveBytes int64) error {
	payload, err := DecodeAndValidate(desc, maxArchiveBytes)
	if err != nil {
		return err
	}
	destDir = strings.TrimSpace(destDir)
	if destDir == "" {
		return fmt.Errorf("destination directory is required")
	}
	cleanDest := filepath.Clean(destDir)
	if err := os.MkdirAll(cleanDest, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	if _, err := walkArchive(payload, defaultMaxExtractedSize, func(name string, header *tar.Header, body io.Reader) error {
		targetPath := filepath.Join(cleanDest, filepath.FromSlash(name))
		if err := ensureWithinRoot(cleanDest, targetPath); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			return os.MkdirAll(targetPath, modeFromTar(header.Mode, 0o755))
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			mode := modeFromTar(header.Mode, 0o644)
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, body); err != nil {
				_ = file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			return os.Chmod(targetPath, mode)
		default:
			return fmt.Errorf("unsupported archive entry type for %q", name)
		}
	}); err != nil {
		return err
	}
	return nil
}

func walkArchive(payload []byte, maxExtractedSize int64, onEntry func(name string, header *tar.Header, body io.Reader) error) (int, error) {
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("open gzip archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	seen := map[string]struct{}{}
	var fileCount int
	var extractedBytes int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("read tar entry: %w", err)
		}
		name, err := sanitizeArchivePath(header.Name)
		if err != nil {
			return 0, fmt.Errorf("invalid tar entry path %q: %w", header.Name, err)
		}
		if _, exists := seen[name]; exists {
			return 0, fmt.Errorf("duplicate tar entry path %q", name)
		}
		seen[name] = struct{}{}

		switch header.Typeflag {
		case tar.TypeDir:
			if onEntry != nil {
				if err := onEntry(name, header, nil); err != nil {
					return 0, fmt.Errorf("apply tar entry %q: %w", name, err)
				}
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 {
				return 0, fmt.Errorf("invalid negative size for %q", name)
			}
			extractedBytes += header.Size
			if maxExtractedSize > 0 && extractedBytes > maxExtractedSize {
				return 0, fmt.Errorf("archive extracted size exceeds limit: %d > %d", extractedBytes, maxExtractedSize)
			}
			limited := &io.LimitedReader{R: tr, N: header.Size}
			if onEntry != nil {
				if err := onEntry(name, header, limited); err != nil {
					return 0, fmt.Errorf("apply tar entry %q: %w", name, err)
				}
			}
			if _, err := io.Copy(io.Discard, limited); err != nil {
				return 0, fmt.Errorf("drain tar entry %q: %w", name, err)
			}
			if limited.N != 0 {
				return 0, fmt.Errorf("tar entry %q truncated", name)
			}
			fileCount++
		default:
			return 0, fmt.Errorf("unsupported tar entry type for %q", name)
		}
	}
	if fileCount == 0 {
		return 0, fmt.Errorf("test assets archive must contain at least one file")
	}
	return fileCount, nil
}

func sanitizeArchivePath(raw string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if strings.ContainsRune(clean, '\x00') {
		return "", fmt.Errorf("path must not contain NUL")
	}
	if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path must be relative and stay within archive root")
	}
	return clean, nil
}

func ensureWithinRoot(root, target string) error {
	rootClean := filepath.Clean(root)
	targetClean := filepath.Clean(target)
	rel, err := filepath.Rel(rootClean, targetClean)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target path escapes destination root: %s", target)
	}
	return nil
}

func modeFromTar(mode int64, fallback fs.FileMode) fs.FileMode {
	perm := fs.FileMode(mode) & fs.ModePerm
	if perm == 0 {
		return fallback
	}
	return perm
}
