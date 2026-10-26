package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func copyTo(writer io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(writer, f)
	return err
}

func writeTar(out, name string, files []string) (err error) {
	f, err := os.Create(filepath.Join(out, name))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	gz := gzip.NewWriter(f)
	defer func() { err = errors.Join(err, gz.Close()) }()
	archive := tar.NewWriter(gz)
	defer func() { err = errors.Join(err, archive.Close()) }()
	for _, name := range files {
		path := filepath.Join(out, name)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if err := copyTo(archive, path); err != nil {
			return err
		}
	}
	return nil
}

func writeZip(out, name string, files []string) (err error) {
	f, err := os.Create(filepath.Join(out, name))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	archive := zip.NewWriter(f)
	defer func() { err = errors.Join(err, archive.Close()) }()
	for _, name := range files {
		writer, err := archive.Create(name)
		if err != nil {
			return err
		}
		if err := copyTo(writer, filepath.Join(out, name)); err != nil {
			return err
		}
	}
	return nil
}
