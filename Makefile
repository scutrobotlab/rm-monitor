build-all:
	go build -trimpath -ldflags "-s -w" -o bin/artifact-cleaner artifact-cleaner/main.go
	go build -trimpath -ldflags "-s -w" -o bin/health-checker health-checker/main.go
	go build -trimpath -ldflags "-s -w" -o bin/highlight-job highlight-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/lark-notifier lark-notifier/main.go
	go build -trimpath -ldflags "-s -w" -o bin/manifest-job manifest-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/match-controller match-controller/main.go
	go build -trimpath -ldflags "-s -w" -o bin/migrate-job migrate-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/record-job record-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/stt-job stt-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/stt-subtitle cmd/stt-subtitle/main.go
	go build -trimpath -ldflags "-s -w" -o bin/transcode-job transcode-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/lark-record-job lark-record-job/main.go
