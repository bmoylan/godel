// Copyright 2016 Palantir Technologies, Inc.
//
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

package publish

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	"gopkg.in/cheggaaa/pb.v1"

	"github.com/palantir/godel/apps/distgo/cmd/dist"
	"github.com/palantir/godel/apps/distgo/params"
	"github.com/palantir/godel/apps/distgo/pkg/slsspec"
	"github.com/palantir/godel/apps/distgo/templating"
)

const (
	pomTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<project xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 http://maven.apache.org/xsd/maven-4.0.0.xsd" xmlns="http://maven.apache.org/POM/4.0.0"
xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
<modelVersion>4.0.0</modelVersion>
<groupId>{{.Publish.GroupID}}</groupId>
<artifactId>{{.ProductName}}</artifactId>
<version>{{.ProductVersion}}</version>
<packaging>{{packagingType}}</packaging>
</project>
`
)

type Publisher interface {
	Publish(buildSpec params.ProductBuildSpec, paths ProductPaths, stdout io.Writer) (string, error)
}

type BasicConnectionInfo struct {
	URL      string
	Username string
	Password string
}

func Run(buildSpecWithDeps params.ProductBuildSpecWithDeps, publisher Publisher, almanacInfo *AlmanacInfo, stdout io.Writer) error {
	buildSpec := buildSpecWithDeps.Spec
	for _, currDistCfg := range buildSpec.Dist {
		// verify that distribution to publish exists
		artifactPath := dist.ArtifactPath(buildSpec, currDistCfg)
		if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
			return errors.Errorf("distribution for %v does not exist at %v", buildSpec.ProductName, artifactPath)
		}

		paths, err := productPath(buildSpecWithDeps, currDistCfg)
		if err != nil {
			return errors.Wrapf(err, "failed to determine product paths")
		}

		artifactURL, err := publisher.Publish(buildSpec, paths, stdout)
		if err != nil {
			return fmt.Errorf("Publish failed for %v: %v", buildSpec.ProductName, err)
		}

		if almanacInfo != nil && artifactURL != "" {
			if err := almanacPublish(artifactURL, *almanacInfo, buildSpec, currDistCfg, stdout); err != nil {
				return fmt.Errorf("Almanac publish failed for %v: %v", buildSpec.ProductName, err)
			}
		}
	}
	return nil
}

func DistsNotBuilt(buildSpecWithDeps []params.ProductBuildSpecWithDeps) []params.ProductBuildSpecWithDeps {
	var distsNotBuilt []params.ProductBuildSpecWithDeps
	for _, currBuildSpecWithDeps := range buildSpecWithDeps {
		currBuildSpec := currBuildSpecWithDeps.Spec
		for _, currDistCfg := range currBuildSpec.Dist {
			artifactPath := dist.ArtifactPath(currBuildSpec, currDistCfg)
			if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
				distsNotBuilt = append(distsNotBuilt, currBuildSpecWithDeps)
			}
		}
	}
	return distsNotBuilt
}

type ProductPaths struct {
	// path of the form "{{GroupID}}/{{ProductName}}/{{ProductVersion}}". For example, "com/group/foo-service/1.0.1".
	productPath  string
	pomFilePath  string
	artifactPath string
}

func productPath(buildSpecWithDeps params.ProductBuildSpecWithDeps, distCfg params.Dist) (ProductPaths, error) {
	buildSpec := buildSpecWithDeps.Spec

	distType, err := packagingType(distCfg.Info.Type())
	if err != nil {
		return ProductPaths{}, err
	}

	funcs := template.FuncMap{
		"packagingType": func() string { return distType },
	}
	t := template.Must(template.New("pom").Funcs(funcs).Parse(pomTemplate))

	pomFileBuf := bytes.Buffer{}
	if err := t.Execute(&pomFileBuf, templating.ConvertSpec(buildSpec, distCfg)); err != nil {
		return ProductPaths{}, errors.Wrapf(err, "failed to execute template")
	}

	pomFilePath := pomFilePath(buildSpec, distCfg)
	if err := ioutil.WriteFile(pomFilePath, pomFileBuf.Bytes(), 0644); err != nil {
		return ProductPaths{}, errors.Wrapf(err, "failed to write POM file to %v", pomFilePath)
	}

	return ProductPaths{
		productPath:  path.Join(path.Join(strings.Split(distCfg.Publish.GroupID, ".")...), buildSpec.ProductName, buildSpec.ProductVersion),
		pomFilePath:  pomFilePath,
		artifactPath: dist.ArtifactPath(buildSpec, distCfg),
	}, nil
}

func packagingType(distType params.DistInfoType) (string, error) {
	switch distType {
	case params.SLSDistType:
		return "sls.tgz", nil
	case params.BinDistType:
		return "tgz", nil
	case params.RPMDistType:
		return "rpm", nil
	default:
		return "", fmt.Errorf("unknown dist type: %v", distType)
	}
}

func (b BasicConnectionInfo) uploadArtifacts(baseURL string, paths ProductPaths, artifactExists artifactExistsFunc, stdout io.Writer) (string, error) {
	artifactURL, err := b.uploadFile(paths.artifactPath, baseURL, paths.artifactPath, artifactExists, stdout)
	if err != nil {
		return artifactURL, err
	}
	if _, err := b.uploadFile(paths.pomFilePath, baseURL, paths.pomFilePath, artifactExists, stdout); err != nil {
		return artifactURL, err
	}
	return artifactURL, nil
}

type fileInfo struct {
	path      string
	bytes     []byte
	checksums checksums
}

type checksums struct {
	SHA1   string
	SHA256 string
	MD5    string
}

func (c checksums) match(other checksums) bool {
	nonEmptyEqual := nonEmptyEqual(c.MD5, other.MD5) || nonEmptyEqual(c.SHA1, other.SHA1) || nonEmptyEqual(c.SHA256, other.SHA256)
	// if no non-empty checksums are equal, checksums are not equal
	if !nonEmptyEqual {
		return false
	}

	// if non-empty checksums are different, treat as suspect and return false
	if nonEmptyDiffer(c.MD5, other.MD5) || nonEmptyDiffer(c.SHA1, other.SHA1) || nonEmptyDiffer(c.SHA256, other.SHA256) {
		return false
	}

	// at least one non-empty checksum was equal, and no non-empty checksums differed
	return true
}

func nonEmptyEqual(s1, s2 string) bool {
	return s1 != "" && s2 != "" && s1 == s2
}

func nonEmptyDiffer(s1, s2 string) bool {
	return s1 != "" && s2 != "" && s1 != s2
}

// function type returns true if the file represented by the given fileInfo object
type artifactExistsFunc func(fi fileInfo, dstFileName, username, password string) bool

func newFileInfo(pathToFile string) (fileInfo, error) {
	bytes, err := ioutil.ReadFile(pathToFile)
	if err != nil {
		return fileInfo{}, errors.Wrapf(err, "Failed to read file %v", pathToFile)
	}

	sha1Bytes := sha1.Sum(bytes)
	sha256Bytes := sha256.Sum256(bytes)
	md5Bytes := md5.Sum(bytes)

	return fileInfo{
		path:  pathToFile,
		bytes: bytes,
		checksums: checksums{
			SHA1:   hex.EncodeToString(sha1Bytes[:]),
			SHA256: hex.EncodeToString(sha256Bytes[:]),
			MD5:    hex.EncodeToString(md5Bytes[:]),
		},
	}, nil
}

func (b BasicConnectionInfo) uploadFile(filePath, baseURL, artifactPath string, artifactExists artifactExistsFunc, stdout io.Writer) (rURL string, rErr error) {
	rawUploadURL := strings.Join([]string{baseURL, path.Base(artifactPath)}, "/")

	fileInfo, err := newFileInfo(filePath)
	if err != nil {
		return rawUploadURL, err
	}

	if artifactExists != nil && artifactExists(fileInfo, path.Base(artifactPath), b.Username, b.Password) {
		fmt.Fprintf(stdout, "File %s already exists at %s, skipping upload.\n", filePath, rawUploadURL)
		return rawUploadURL, nil
	}

	uploadURL, err := url.Parse(rawUploadURL)
	if err != nil {
		return rawUploadURL, errors.Wrapf(err, "Failed to parse %v as URL", rawUploadURL)
	}

	fmt.Fprintf(stdout, "Uploading %v to %v\n", fileInfo.path, rawUploadURL)

	header := http.Header{}
	addChecksumToHeader(header, "Md5", fileInfo.checksums.MD5)
	addChecksumToHeader(header, "Sha1", fileInfo.checksums.SHA1)
	addChecksumToHeader(header, "Sha256", fileInfo.checksums.SHA256)

	bar := pb.New(len(fileInfo.bytes)).SetUnits(pb.U_BYTES)
	bar.Output = stdout
	bar.SetMaxWidth(120)
	bar.Start()
	defer bar.Finish()
	reader := bar.NewProxyReader(bytes.NewReader(fileInfo.bytes))

	req := http.Request{
		Method:        http.MethodPut,
		URL:           uploadURL,
		Header:        header,
		Body:          ioutil.NopCloser(reader),
		ContentLength: int64(len(fileInfo.bytes)),
	}
	req.SetBasicAuth(b.Username, b.Password)

	resp, err := http.DefaultClient.Do(&req)
	if err != nil {
		return rawUploadURL, errors.Wrapf(err, "failed to upload %v to %v", fileInfo.path, rawUploadURL)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil && rErr == nil {
			rErr = errors.Wrapf(err, "failed to close response body for URL %s", rawUploadURL)
		}
	}()

	if resp.StatusCode >= http.StatusBadRequest {
		msg := fmt.Sprintf("uploading %v to %v resulted in response %q", fileInfo.path, rawUploadURL, resp.Status)
		if body, err := ioutil.ReadAll(resp.Body); err == nil {
			bodyStr := string(body)
			if bodyStr != "" {
				msg += ":\n" + bodyStr
			}
		}
		return rawUploadURL, fmt.Errorf(msg)
	}

	return rawUploadURL, nil
}

func addChecksumToHeader(header http.Header, checksumName string, checksum string) {
	header.Add(fmt.Sprintf("X-Checksum-%v", checksumName), checksum)
}

func pomFilePath(buildSpec params.ProductBuildSpec, distCfg params.Dist) string {
	outputDir := path.Join(buildSpec.ProjectDir, distCfg.OutputDir)
	values := slsspec.TemplateValues(buildSpec.ProductName, buildSpec.ProductVersion)
	outputSlsDir := slsspec.New().RootDirName(values)
	return path.Join(outputDir, path.Base(outputSlsDir)+".pom")
}
