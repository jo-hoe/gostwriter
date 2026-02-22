include help.mk

# get root dir
ROOT_DIR := $(dir $(realpath $(lastword $(MAKEFILE_LIST))))

# Image/registry config
IMAGE_NAME := gostwriter
IMAGE_VERSION := latest
LOCAL_REGISTRY := localhost:5000
LOCAL_REGISTRY_HELM := registry.localhost:5000

# Default goal keeps existing docker-compose start behavior
.DEFAULT_GOAL := start

DOCKER_COMPOSE_CMD := docker compose up

.PHONY: update
update: ## update dependencies
	git pull
	go mod tidy

.PHONY: lint
lint: ## run linters
	golangci-lint run -E dupl -E gocyclo -E gosec -E misspell -E sqlclosecheck

.PHONY: test
test: ## run go tests with coverage
	go test ./... -covermode=count

.PHONY: start
start: ## start via docker-compose
	@${DOCKER_COMPOSE_CMD}

.PHONY: start-rebuild
start-rebuild: ## rebuild and start via docker-compose
	@${DOCKER_COMPOSE_CMD} --build

# ----------------------
# Helm / k3d local dev
# ----------------------

.PHONY: generate-helm-docs
generate-helm-docs: ## re-generates helm docs using docker (if chart has README templates)
	@docker run --rm --volume "$(ROOT_DIR)charts:/helm-docs" jnorwood/helm-docs:latest

.PHONY: start-cluster
start-cluster: ## starts k3d cluster and local registry
	@k3d cluster create --config ${ROOT_DIR}dev/clusterconfig.yaml

.PHONY: stop-k3d
stop-k3d: ## stop k3d cluster and local registry
	@k3d cluster delete --config ${ROOT_DIR}dev/clusterconfig.yaml

.PHONY: restart-k3d
restart-k3d: stop-k3d start-k3d ## restarts k3d cluster and re-installs chart

.PHONY: push-k3d
push-k3d: ## build and push docker image to local registry
	@docker build -f ${ROOT_DIR}Dockerfile . -t ${IMAGE_NAME}:${IMAGE_VERSION}
	@docker tag ${IMAGE_NAME}:${IMAGE_VERSION} ${LOCAL_REGISTRY}/${IMAGE_NAME}:${IMAGE_VERSION}
	@docker push ${LOCAL_REGISTRY}/${IMAGE_NAME}:${IMAGE_VERSION}

.PHONY: start-k3d
start-k3d: start-cluster push-k3d ## create k3d cluster, push local image, install helm chart with dev values
	@helm install ${IMAGE_NAME} ${ROOT_DIR}charts/${IMAGE_NAME} \
		--set image.repository=${LOCAL_REGISTRY_HELM}/${IMAGE_NAME} \
		--set image.tag=${IMAGE_VERSION} \
		-f ${ROOT_DIR}dev/config.yaml

.PHONY: upgrade-k3d
upgrade-k3d: push-k3d ## upgrade Helm release with latest local image & dev values
	@helm upgrade ${IMAGE_NAME} ${ROOT_DIR}charts/${IMAGE_NAME} \
		--set image.repository=${LOCAL_REGISTRY_HELM}/${IMAGE_NAME} \
		--set image.tag=${IMAGE_VERSION} \
		-f ${ROOT_DIR}dev/config.yaml

.PHONY: uninstall-k3d
uninstall-k3d: ## uninstall Helm release from k3d cluster
	@helm uninstall ${IMAGE_NAME}