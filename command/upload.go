// Package command for upload
// Upload puts the archive file into an S3 bucket
package command

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/briandowns/spinner"
	"github.com/hashicorp/go-hclog"
	"github.com/mitchellh/cli"
	"github.com/ryanuber/columnize"
)

const (
	archiveFileDefault = "rover.zip"
	archiveFileDescr   = "Archive filename"
)

// UploadCommand describes upload related fields
type UploadCommand struct {
	AccessKey   string
	ArchiveFile string
	Bucket      string
	HostName    string
	OS          string
	Prefix      string
	Region      string
	SecretKey   string
	Token       string
	UI          cli.Ui
}

// Help output
func (c *UploadCommand) Help() string {
	helpText := `
Usage: rover upload [options]
  Upload an archive file to S3 bucket

General Options:
  -file="rover-host-20171028110212.zip"	Specify the filename to upload.

Environment Variables:

  The upload command requires these environment variables:

  - AWS_ACCESS_KEY_ID
  - AWS_SECRET_ACCESS_KEY
  - AWS_BUCKET
  - AWS_REGION

  Optionally specify a bucket prefix:

  - AWS_PREFIX
`

	return strings.TrimSpace(helpText)
}

// Run command
func (c *UploadCommand) Run(args []string) int {
	c.OS = runtime.GOOS
	h, err := GetHostName()
	if err != nil {
		out := fmt.Sprintf("Cannot get system hostname with error %v", err)
		c.UI.Output(out)

		return 1
	}
	c.HostName = h
	// Internal logging
	l := "rover.log"
	p := filepath.Join(fmt.Sprintf("%s", c.HostName), "log")
	if err := os.MkdirAll(p, os.ModePerm); err != nil {
		fmt.Println(fmt.Sprintf("Cannot create log directory %s.", p))
		return 1
	}
	f, err := os.OpenFile(filepath.Join(p, l), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println(fmt.Sprintf("Failed to open log file %s with error: %v", f, err))
		return 1
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	logger := hclog.New(&hclog.LoggerOptions{Name: "rover", Level: hclog.LevelFromString("INFO"), Output: w})
	logger.Info("upload", "hello from the Upload module at", c.HostName)
	logger.Info("upload", "our detected OS", c.OS)
	cmdFlags := flag.NewFlagSet("upload", flag.ContinueOnError)
	cmdFlags.Usage = func() { c.UI.Output(c.Help()) }
	cmdFlags.StringVar(&c.ArchiveFile, "file", archiveFileDefault, archiveFileDescr)
	if err := cmdFlags.Parse(args); err != nil {
		return 1
	}
	c.AccessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	c.SecretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	c.Bucket = os.Getenv("AWS_BUCKET")
	c.Prefix = os.Getenv("AWS_PREFIX")
	c.Region = os.Getenv("AWS_REGION")
	c.Token = ""
	if len(c.AccessKey) == 0 || len(c.SecretKey) == 0 || len(c.Bucket) == 0 || len(c.Region) == 0 {
		logger.Error("missing at least one of the required AWS credential environment variables")
		columns := []string{}
		kvs := map[string]string{"AWS_ACCESS_KEY_ID": "Access key ID for AWS", "AWS_SECRET_ACCESS_KEY": "Secret access key ID for AWS", "AWS_BUCKET": " Name of the S3 bucket", "AWS_REGION": "AWS region for the bucket"}
		for k, v := range kvs {
			columns = append(columns, fmt.Sprintf("%s: | %s ", k, v))
		}
		envVars := columnize.SimpleFormat(columns)
		out := fmt.Sprintf("One or more required environment variables are not set;\n please ensure that the following environment variables are set:\n\n%s", envVars)
		c.UI.Error(out)
		return 1
	}
	creds := credentials.NewStaticCredentials(c.AccessKey, c.SecretKey, c.Token)
	cfg := aws.NewConfig().WithRegion(c.Region).WithCredentials(creds)
	svc := s3.New(session.New(), cfg)
	file, err := os.Open(c.ArchiveFile)
	if err != nil {
		out := fmt.Sprintf("Error opening %s! Error: %v", c.ArchiveFile, err)
		c.UI.Error(out)
		logger.Error("upload", "error", out)
		return 1
	}
	defer func() {
		// Close after zip file is successfully uploaded
		err = file.Close()
		if err != nil {
			out := fmt.Sprintf("Could not close %s! Error: %v", c.ArchiveFile, err)
			c.UI.Error(out)
			os.Exit(1)
		}
	}()
	fileInfo, err := file.Stat()
	if err != nil {
		out := fmt.Sprintf("Could not stat file %s! Error: %v", c.ArchiveFile, err)
		c.UI.Error(out)
		return 1
	}
	var fileSize int64 = fileInfo.Size()
	buffer := make([]byte, fileSize)
	defer func() {
		// Read from the buffer
		_, err = file.Read(buffer)
		if err != nil {
			out := fmt.Sprintf("Could not read buffer! Error: %s", err)
			logger.Error("upload", "error", err.Error())
			c.UI.Error(out)
			os.Exit(1)
		}
	}()
	path := fmt.Sprintf("%s/%s", c.Prefix, file.Name())
	fileBytes := bytes.NewReader(buffer)
	// For more than application/zip later
	fileType := http.DetectContentType(buffer)
	params := &s3.PutObjectInput{
		Bucket:        aws.String(c.Bucket),
		Key:           aws.String(path),
		Body:          fileBytes,
		ContentLength: aws.Int64(fileSize),
		ContentType:   aws.String(fileType),
	}
	// Shout out to Ye Olde School BSD spinner!
	roverSpinnerSet := []string{"/", "|", "\\", "-", "|", "\\", "-"}
	s := spinner.New(roverSpinnerSet, 174*time.Millisecond)
	s.Writer = os.Stderr
	err = s.Color("fgHiCyan")
	if err != nil {
		logger.Warn("upload", "weird-error", err.Error())
	}
	s.Suffix = " Gathering Vault information ..."
	s.FinalMSG = fmt.Sprintf("Success! Uploaded s3://%s/%s", c.Bucket, file.Name())
	s.Start()

	resp, err := svc.PutObject(params)
	if err != nil {
		out := fmt.Sprintf("Error: %s from AWS! Response: %s", err, resp)
		c.UI.Error(out)
	}
	s.Stop()

	return 0

}

// Synopsis output
func (c *UploadCommand) Synopsis() string {
	return "Uploads rover archive file to S3 bucket"
}
