package core

import (
	"fmt"
	"sync"
	"time"

	"github.com/golang/glog"
)

func (t *CleanupTask) ImageCleanupLoop(done chan struct{}, wg *sync.WaitGroup) {
	go func() {
		kubeClient, err := NewKubernetesClient(t.KubeConfig)
		if err != nil {
			glog.Fatalf("Cannot create Kubernetes client: %v", err)
		}

		ecrClient, err := NewECRClient(t.AwsRegion)
		if err != nil {
			glog.Fatalf("Cannot create ECR client: %v", err)
		}

		for {
			select {
			case <-time.After(time.Duration(t.Interval) * time.Minute):
				errors := t.RemoveOldImages(kubeClient, ecrClient)
				if len(errors) > 0 {
					for _, err := range errors {
						glog.Error(err)
					}
				}
			case <-done:
				wg.Done()
				glog.Info("Stopped deployment status watcher.")
				return
			}
		}
	}()
}

func (t *CleanupTask) RemoveOldImages(kubeClient KubernetesClient, ecrClient ECRClient) []error {
	errors := []error{}

	glog.Info("Cleanup loop started.")

	pods, err := kubeClient.ListAllPods(t.KubeNamespaces)
	if err != nil {
		errors = append(errors, fmt.Errorf("Cannot list pods: %v", err))
		return errors
	}
	glog.Infof("There are currently %d running pods.", len(pods))

	usedImages := ECRImagesFromPods(t.AwsRegion, pods)
	glog.Infof("There are currently %d ECR images in use.", len(usedImages))

	repos, err := ecrClient.ListRepositories(t.EcrRepositories)
	if err != nil {
		errors = append(errors, fmt.Errorf("Cannot list ECR repositories: %v", err))
		return errors
	}

	for _, repo := range repos {
		repoName := *repo.RepositoryName
		glog.Infof("Processing '%s' ECR repo.", repoName)

		images, err := ecrClient.ListImages(&repoName)
		if err != nil {
			errors = append(errors, fmt.Errorf("Cannot list images from repo '%s': %v", repoName, err))
			continue
		}
		glog.Infof("Number of images in ECR repo: %d", len(images))

		unusedImages := FilterOldUnusedImages(t.MaxImages, images, usedImages[repoName])

		if len(unusedImages) == 0 {
			glog.Info("There's no old unused images to remove. Continuing.")
			continue
		}

		glog.Infof("Removing %d old unused images.", len(unusedImages))
		if err = ecrClient.BatchRemoveImages(&repoName, unusedImages); err != nil {
			errors = append(errors, fmt.Errorf("Could not batch remove images from repo '%s': %v", repoName, err))
			continue
		}
	}

	glog.Info("Cleanup loop finished.")

	return errors
}
