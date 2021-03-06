/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sidecar

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/test-infra/prow/gcsupload"
	"k8s.io/test-infra/prow/pod-utils/wrapper"
	"k8s.io/test-infra/prow/secretutil"
	"k8s.io/test-infra/prow/testutil"
)

func TestCensor(t *testing.T) {
	preamble := func() string {
		return `In my younger and more vulnerable years my father gave me some advice that I’ve been turning over in my mind ever since.`
	}

	var testCases = []struct {
		name          string
		input, output string
		secrets       []string
		bufferSize    int
	}{
		{
			name:       "input smaller than buffer size",
			input:      preamble()[:100],
			secrets:    []string{"younger", "my"},
			output:     "In ** ******* and more vulnerable years ** father gave me some advice that I’ve been turning over ",
			bufferSize: 200,
		},
		{
			name:       "input larger than buffer size, not a multiple",
			input:      preamble()[:100],
			secrets:    []string{"younger", "my"},
			output:     "In ** ******* and more vulnerable years ** father gave me some advice that I’ve been turning over ",
			bufferSize: 16,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			censorer := secretutil.NewCensorer()
			censorer.Refresh(testCase.secrets...)
			input := ioutil.NopCloser(bytes.NewBufferString(testCase.input))
			outputSink := &bytes.Buffer{}
			output := nopWriteCloser(outputSink)
			if err := censor(input, output, censorer, testCase.bufferSize); err != nil {
				t.Fatalf("expected no error from censor, got %v", err)
			}
			if diff := cmp.Diff(outputSink.String(), testCase.output); diff != "" {
				t.Fatalf("got incorrect output after censoring: %v", diff)
			}
		})
	}

}

func nopWriteCloser(w io.Writer) io.WriteCloser {
	return &nopCloser{Writer: w}
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error { return nil }

const inputDir = "testdata/input"

func TestCensorIntegration(t *testing.T) {
	// copy input to a temp dir so we don't touch the golden input files
	tempDir := t.TempDir()
	if err := filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		relpath, _ := filepath.Rel(inputDir, path) // this errors when it's not relative, but that's known here
		dest := filepath.Join(tempDir, relpath)
		if info.IsDir() {
			return os.MkdirAll(dest, info.Mode())
		}
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		defer func() {
			if err := out.Close(); err != nil {
				t.Fatalf("could not close output file: %v", err)
			}
		}()
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			if err := in.Close(); err != nil {
				t.Fatalf("could not close input file: %v", err)
			}
		}()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("failed to copy input to temp dir: %v", err)
	}

	bufferSize := 1
	options := Options{
		GcsOptions: &gcsupload.Options{
			Items: []string{filepath.Join(tempDir, "artifacts")},
		},
		Entries: []wrapper.Options{
			{ProcessLog: filepath.Join(tempDir, "logs/one.log")},
			{ProcessLog: filepath.Join(tempDir, "logs/two.log")},
		},
		SecretDirectories: []string{"testdata/secrets"},
		// this will be smaller than the size of a secret, so this tests our buffer calculation
		CensoringBufferSize: &bufferSize,
	}
	if err := options.censor(); err != nil {
		t.Fatalf("got an error from censoring: %v", err)
	}

	testutil.CompareWithFixtureDir(t, "testdata/output", tempDir)
}
