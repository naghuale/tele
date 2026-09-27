Closes #

## Что изменено

## Как проверено

- [ ] сначала тест, который падает без исправления
- [ ] `gofmt -l .` пусто, `go vet ./...` чисто
- [ ] `go test -race -count=1 ./...`
- [ ] `golangci-lint run ./...`
- [ ] с настоящей TDLib (`TELECLI_TDLIB_LIBRARY=...`), если затронут `internal/telegram` или `internal/application`

## Риски и что не сделано
