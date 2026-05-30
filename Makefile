build-all:
	go build -trimpath -ldflags "-s -w" -o bin/artifact-cleaner artifact-cleaner/main.go
	go build -trimpath -ldflags "-s -w" -o bin/highlight-artifact-job highlight-artifact-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/highlight-dispatcher highlight-dispatcher/main.go
	go build -trimpath -ldflags "-s -w" -o bin/lark-notifier lark-notifier/main.go
	go build -trimpath -ldflags "-s -w" -o bin/manifest-job manifest-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/migrate-job migrate-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/monitor monitor/main.go
	go build -trimpath -ldflags "-s -w" -o bin/ocr-dispatcher ocr-dispatcher/main.go
	go build -trimpath -ldflags "-s -w" -o bin/record-dispatcher record-dispatcher/main.go
	go build -trimpath -ldflags "-s -w" -o bin/record-job record-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/stt-job stt-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/stt-normalize cmd/stt-normalize/main.go
	go build -trimpath -ldflags "-s -w" -o bin/stt-subtitle cmd/stt-subtitle/main.go
	go build -trimpath -ldflags "-s -w" -o bin/transcode-dispatcher transcode-dispatcher/main.go
	go build -trimpath -ldflags "-s -w" -o bin/transcode-job transcode-job/main.go
	go build -trimpath -ldflags "-s -w" -o bin/uploader-dispatcher uploader-dispatcher/main.go
	go build -trimpath -ldflags "-s -w" -o bin/uploader-job uploader-job/main.go
