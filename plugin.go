package main

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"sync"

	"github.com/honestbee/drone-helm-repo/pkg/storage"
	"github.com/honestbee/drone-helm-repo/pkg/storage/s3"
	"github.com/honestbee/drone-helm-repo/pkg/util"
)

var supportedStorageSchemes = [...]string{
	"s3",
}

type (
	// Config maps the Drone plugin parameters
	Config struct {
		SourceDir    string   `json:"source_dir"`
		Exclude      []string `json:"exclude"`
		StorageURL   string   `json:"storage_url"`
		RepoURL      string   `json:"repo_url"`
		Debug        bool     `json:"debug"`
		AWSAccessKey string   `json:"aws_access_key"`
		AWSSecretKey string   `json:"aws_secret_key"`
		AWSRegion    string   `json:"aws_region"`
	}
	// Plugin implements this Drone plugin functionality
	Plugin struct {
		Config Config
	}
)

// Exec will run the Drone plugin
func (p *Plugin) Exec() error {
	if p.Config.Debug {
		p.debug()
	}

	//validate plugin config
	err := validateConfig(p.Config)
	if err != nil {
		return err
	}

	destinationURL, _ := url.Parse(p.Config.StorageURL)
	// get a temp dir to store generated packages
	tempDir, err := ioutil.TempDir("./", "tempOutput")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir) // clean up

	charts := util.FindCharts(p.Config.SourceDir, p.Config.Exclude, logger)
	packages := util.PackageCharts(charts, tempDir, logger, p.Config.RepoURL)
	//upload charts
	var objectStore storage.ObjectStore
	switch destinationURL.Scheme {
	case "s3":
		objectStore, err = s3.CreateS3ObjectStore(
			&s3.Config{
				AccessKey: p.Config.AWSAccessKey,
				SecretKey: p.Config.AWSSecretKey,
				Region:    p.Config.AWSRegion,
				S3URI:     p.Config.StorageURL,
			})
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("protocol %q not implemented yet", destinationURL.Scheme)
	}
	_, err = storeFiles(objectStore, packages, logger)
	return err
}

func (p *Plugin) debug() {
	fmt.Println(p)
	// debug env vars
	for _, e := range os.Environ() {
		fmt.Println("-Var:--", e)
	}
	// debug plugin obj
	fmt.Printf("Source Dir: %s \n", p.Config.SourceDir)
	fmt.Printf("Excluded Directories: %v \n", p.Config.Exclude)
	fmt.Printf("Storage URL: %s \n", p.Config.StorageURL)
	fmt.Printf("Repo URL: %s \n", p.Config.RepoURL)
}

func validateConfig(conf Config) error {
	destinationURL, err := url.Parse(conf.StorageURL)
	if err != nil {
		return fmt.Errorf("could not parse storage-url %q", conf.StorageURL)
	}
	for _, s := range supportedStorageSchemes {
		if destinationURL.Scheme == s {
			if s == "s3" {
				//more conditions to validate
				if conf.AWSRegion == "" {
					return fmt.Errorf("--aws-region required for s3 storage")
				}
			}
			break
		}
		return fmt.Errorf("storage-url does not have valid protocol %q, should be in %v", destinationURL.Scheme, supportedStorageSchemes)
	}
	return nil
}

func storeFiles(storage storage.ObjectStore, in chan *util.FileStat, logger *util.Logger) (int, error) {
	concurrency := 5
	var wg sync.WaitGroup

	storedFilesCount := 0
	var outerError error
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range in {
				if file.Err != nil {
					logger.Err.Println(file.Err)
					continue
				}
				err := storage.StoreFile(file, logger)
				if err != nil {
					outerError = err
					return //bail worker as soon one upload fails
				}
				storedFilesCount++
			}
		}()
	}
	wg.Wait()
	return storedFilesCount, outerError
}
