# gostwriter

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

Helm chart for deploying Gostwriter

## Source Code

* <https://github.com/jo-hoe/gostwriter>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for Pod scheduling |
| configAsSecret | bool | `true` | Provide application configuration via file only (never env): By default, the chart renders the config as a Secret and mounts it at /app/config/config.yaml |
| configRaw | string | `""` |  |
| cronjob | object | `{"annotations":{},"args":[],"backoffLimit":1,"command":[],"concurrencyPolicy":"Forbid","enabled":false,"env":[],"failedJobsHistoryLimit":1,"image":{"pullPolicy":"","repository":"","tag":""},"labels":{},"resources":{},"schedule":"0 2 * * *","startingDeadlineSeconds":null,"successfulJobsHistoryLimit":1,"timeZone":""}` | Optional Kubernetes CronJob for scheduled tasks This chart does not define any default job logic. Configure command/args as needed. |
| cronjob.backoffLimit | int | `1` | Job backoff limit |
| cronjob.command | list | `[]` | Container command/args for the CronJob container (required for actual work) |
| cronjob.concurrencyPolicy | string | `"Forbid"` | Concurrency policy: Allow | Forbid | Replace |
| cronjob.enabled | bool | `false` | Enable rendering a CronJob resource |
| cronjob.env | list | `[]` | Optional environment variables for the CronJob container |
| cronjob.failedJobsHistoryLimit | int | `1` | How many failed jobs to keep |
| cronjob.image | object | `{"pullPolicy":"","repository":"","tag":""}` | Image overrides for CronJob (fallback to top-level image when empty) |
| cronjob.labels | object | `{}` | Additional labels/annotations for the CronJob |
| cronjob.resources | object | `{}` | Optional resource requests/limits |
| cronjob.schedule | string | `"0 2 * * *"` | Cron schedule (standard CRON format) |
| cronjob.startingDeadlineSeconds | string | `nil` | Starting deadline seconds for missed schedules (omit or set to null to disable) |
| cronjob.successfulJobsHistoryLimit | int | `1` | How many completed jobs to keep |
| cronjob.timeZone | string | `""` | Optional Kubernetes CronJob timezone (K8s v1.27+), e.g. "Europe/Berlin" When set, renders .spec.timeZone in the CronJob. |
| existingConfigSecret | string | `""` | Reference an existing Secret that contains a `config.yaml` key (overrides chart-generated Secret/ConfigMap) |
| fullnameOverride | string | `""` | Fully override the release name |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"ghcr.io/jo-hoe/gostwriter"` |  |
| image.tag | string | `""` |  |
| imagePullSecrets | list | `[]` | Secrets to use for pulling images (for private registries) |
| ingress.annotations | object | `{}` | Annotations to add to the Ingress |
| ingress.className | string | `""` | IngressClass name |
| ingress.enabled | bool | `false` | Enable Ingress |
| ingress.hosts | list | `[{"host":"gostwriter.local","paths":[{"path":"/","pathType":"Prefix"}]}]` | Ingress host definitions |
| ingress.tls | list | `[]` | TLS configuration for the Ingress |
| llm.aiproxy.apiKey | string | `""` | API key for the AI Proxy (optional) |
| llm.aiproxy.baseUrl | string | `"http://localhost:8900"` | Base URL for AI Proxy (OpenAI-compatible) endpoint |
| llm.aiproxy.instructions | string | `""` | Optional instructions prompt override |
| llm.aiproxy.maxTokens | int | `0` | Maximum tokens for responses (0 uses provider default) |
| llm.aiproxy.model | string | `"gpt-5"` | Model name to use |
| llm.aiproxy.systemPrompt | string | `""` | Optional system prompt override |
| llm.aiproxy.temperature | int | `0` | Sampling temperature |
| llm.mock.delay | string | `"2s"` | Artificial delay for mock responses |
| llm.mock.prefix | string | `"Transcribed by Mock"` | Prefix added by the mock provider |
| llm.provider | string | `"mock"` | Provider selection: "mock" or "aiproxy" |
| nameOverride | string | `""` | Partially override the chart name |
| nodeSelector | object | `{}` | Node selector for Pod assignment |
| persistence | object | `{"accessModes":["ReadWriteOnce"],"enabled":false,"existingClaim":"","size":"1Gi","storageClass":""}` | Persistence for /app/data (SQLite DB and git clone cache) |
| podAnnotations | object | `{}` | Annotations to add to the Pod |
| podLabels | object | `{}` | Additional labels to add to the Pod |
| podSecurityContext | object | `{}` | Pod-level security context |
| replicaCount | int | `1` | Number of desired pod replicas |
| resources | object | `{}` | Resource requests and limits for the container |
| securityContext | object | `{}` | Container-level security context |
| server | object | `{"address":":8080","apiKey":"","callbackBackoff":"2s","callbackRetries":3,"databasePath":"","idleTimeout":"60s","maxUploadSize":"10Mi","readTimeout":"15s","shutdownGrace":"15s","storageDir":"/app/data","workerCount":4,"writeTimeout":"2m"}` | Structured configuration rendered into config.yaml (used only when configRaw is empty) |
| server.address | string | `":8080"` | HTTP bind address |
| server.apiKey | string | `""` | Optional static API key required via X-API-Key header |
| server.callbackBackoff | string | `"2s"` | Base backoff duration between callback retries |
| server.callbackRetries | int | `3` | Number of times to retry webhook callbacks |
| server.databasePath | string | `""` | SQLite DB path; default storageDir/gostwriter.db if empty |
| server.idleTimeout | string | `"60s"` | Keep-alive idle timeout for connections (no effect on in-flight requests) |
| server.maxUploadSize | string | `"10Mi"` | Max allowed upload size (e.g., 10Mi, 20MB) |
| server.readTimeout | string | `"15s"` | Maximum time to read the entire request (headers + body) |
| server.shutdownGrace | string | `"15s"` | Grace period on shutdown to wait for workers to finish |
| server.storageDir | string | `"/app/data"` | Directory inside the container where data is stored (DB, git cache) |
| server.workerCount | int | `4` | Number of worker goroutines processing jobs |
| server.writeTimeout | string | `"2m"` | Maximum time to process and write the response for a request |
| service.port | int | `80` | Service port |
| service.targetPort | int | `8080` | Target container port exposed by the application |
| service.type | string | `"ClusterIP"` | Kubernetes Service type |
| serviceAccount.annotations | object | `{}` | Annotations to add to the service account |
| serviceAccount.automount | bool | `true` | Automatically mount a ServiceAccount's API credentials |
| serviceAccount.create | bool | `true` | Specifies whether a service account should be created |
| serviceAccount.name | string | `""` | The name of the service account to use. If not set and create is true, a name is generated using the fullname template |
| target | object | `{"github":{"apiBaseUrl":"https://api.github.com","auth":{"token":""},"authorEmail":"bot@example.com","authorName":"Gostwriter Bot","basePath":"inbox/","branch":"main","commitMessageTemplate":"Add transcription {{ .JobID }}","enabled":true,"filenameTemplate":"{{ .Timestamp.Format \"20060102-150405\" }}-{{ .JobID }}.md","repositoryName":"yourrepo","repositoryOwner":"yourorg"}}` | Single target configuration (GitHub via REST API) IMPORTANT: For Kubernetes, do NOT use env expansion inside the config. Provide the token directly inside a Secret-backed config.yaml (via configRaw or existingConfigSecret). |
| target.github | object | `{"apiBaseUrl":"https://api.github.com","auth":{"token":""},"authorEmail":"bot@example.com","authorName":"Gostwriter Bot","basePath":"inbox/","branch":"main","commitMessageTemplate":"Add transcription {{ .JobID }}","enabled":true,"filenameTemplate":"{{ .Timestamp.Format \"20060102-150405\" }}-{{ .JobID }}.md","repositoryName":"yourrepo","repositoryOwner":"yourorg"}` | GitHub target configuration (REST API) |
| target.github.apiBaseUrl | string | `"https://api.github.com"` | Optional override for the GitHub API base URL (e.g., for GH Enterprise) |
| target.github.auth.token | string | `""` | Personal access token for GitHub REST API |
| target.github.authorEmail | string | `"bot@example.com"` | Commit author email used for the commit metadata |
| target.github.authorName | string | `"Gostwriter Bot"` | Commit author name used for the commit metadata |
| target.github.basePath | string | `"inbox/"` | Base path in the repository where files are written |
| target.github.branch | string | `"main"` | Branch to use for commits |
| target.github.commitMessageTemplate | string | `"Add transcription {{ .JobID }}"` | Go text/template for commit message |
| target.github.enabled | bool | `true` | Enable/disable the GitHub target |
| target.github.filenameTemplate | string | `"{{ .Timestamp.Format \"20060102-150405\" }}-{{ .JobID }}.md"` | Go text/template for filename; has .Timestamp, .JobID, etc. |
| target.github.repositoryOwner | string | `"yourorg"` | GitHub repository owner and name |
| tolerations | list | `[]` | Tolerations for Pod assignment |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
