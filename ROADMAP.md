# ROADMAP: Vecura (Wails v2 + RAM Vector Search)

Пошаговый план реализации на основе `ARCHITECTURE.md`. Каждый этап — законченный, тестируемый инкремент. Код пишется в порядке от фундамента (БД, схема) к ядру (векторный движок), затем к инференсу (провайдеры) и, наконец, к GUI/IPC.

Принцип: **не смешивать слои** — на каждом этапе модуль покрывается юнит-тестами до перехода к следующему.

---

## Этап 0. Инициализация проекта

[x] `go mod init vecura` в `C:\Users\yulta\Documents\.dev\Vecura`.
[x] Добавить зависимости: `modernc.org/sqlite`, `wailsapp/wails/v2`, `go-skynet/go-llama.cpp` (опц. локальный инференс).
[x] Создать структуру каталогов:
  ```
  internal/
    db/          # SQLite слой
    vector/      # In-memory vector store
    embedder/    # Embedder interface + local/remote
    models/      # менеджер моделей
    scan/        # watcher + pipeline
    api/         # Wails App struct + методы
  ```
[x] Завести `AGENTS.md` с командами lint/typecheck/test.

**Критерий готовности:** проект собирается `go build ./...`, пустой Wails-каркас стартует.

---

## Этап 1. Слой базы данных (`internal/db`)

[x] Реализовать миграции из раздела 2 `ARCHITECTURE.md` (tables `images`, `tags`, `image_tags`, `embeddings` + индексы).
[x] Репозиторий `ImageRepo`: `UpsertImage`, `GetImagePath`, `AddTag`, `GetByTag`.
[x] Репозиторий `EmbeddingRepo`:
  - `BatchInsertEmbeddings([]EmbeddingRow)` — WAL-транзакция, bulk insert (критика №5).
  - `LoadAllEmbeddings()` — возвращает строки для boot (без копии BLOB, см. критика №4).
  - `GetEmbeddingsByModel(provider, modelID)`.
[x] Хелпер `blobToFloat32(blob)` через `unsafe.Slice` (без аллокации).
[x] Хелпер `float32ToBlob(vec)` для записи.

**Тесты:** round-trip insert/load, проверка `unsafe.Slice` совпадения байт, bulk insert на 10k строк.

---

## Этап 2. In-Memory векторный движок (`internal/vector`)

[x] Типы `ModelVectors` (preallocate + `Count`, без `append`) и `VectorStore` (map + `atomic.Pointer[storeSnapshot]`).
[x] `VectorStore.BuildFromRows(rows)` — параллельный decode BLOB → flat-array, группировка по `(provider, model_id)`, вычисление `InvNorm` (критика №2).
[x] `Add(imageID, provider, modelID, vec)` — под `Lock()`, рост `Cap` ×1.5, `snap.Store` (критика №1).
[x] `Search(Q, provider, modelID, K)`:
  - выбор стора из snapshot (без блокировки на весь поиск),
  - параллельный Dot×InvNorm по сегментам `runtime.NumCPU()`,
  - **локальные Min-Heap на сегмент + merge** (критика №3).
[x] Бенчмарк `Search` на синтетике 100k × 2048 (<30 мс).

**Тесты:** correctness (Top-K совпадает с naive), concurrency (search во время Add не паникует/не даёт мусор), benchmark.

---

## Этап 3. Слой провайдеров эмбеддингов (`internal/embedder`)

[x] Интерфейс `Embedder { Key(), Dim(), Embed([]string) ([][]float32, error) }`.
[x] `OpenAICompatibleEmbedder` (pure Go HTTP): `BaseURL`, `APIKey`, `Model`; batch-вызовы, `http.Client` с `MaxIdleConns` (критика В).
- [ ] `LocalLlamaEmbedder` (CGO, опционально): обёртка `llama.cpp`, `defer C.free` (критика В).
[x] Кэш `hash(file + prompt + model)` для пропуска повторного инференса (раздел 5).
[x] Батчер + очередь для remote (rate limit / стоимость, раздел 5).

**Тесты:** mock HTTP-сервер для `OpenAICompatibleEmbedder`, проверка Dim, размер батча, кэш-hit.

---

## Этап 4. Менеджер моделей (`internal/models`)

[x] Registry `map[modelKey]*LoadedModel` под `sync.RWMutex`.
[x] `RegisterRemote(cfg)` — добавление remote-провайдера по API (раздел 5).
- [ ] `LoadLocal(path, config)` — `llama_load_model`, RAM-budget проверка.
[x] `Unload(key)` + **LRU-eviction** при превышении бюджета RAM (раздел 5, 7).
- [ ] Валидация GGUF перед загрузкой (безопасность, раздел 5).
[x] `GetEmbedder(key)` — возвращает нужный `Embedder` для пайплайна.

**Тесты:** load/unload, eviction вытесняет старую модель, registry thread-safe.

---

## Этап 5. Сканер и пайплайн (`internal/scan`)

- [ ] Watcher на папку (fsnotify или native), детект новых изображений.
[x] Pipeline: `image → prompt/metadata → Embedder.Embed → BatchInsertEmbeddings → VectorStore.Add`.
[x] Генерация превью `.webp` 256×256 в `~/.vecura/thumbnails/` (раздел 6.Б).
[x] Обработка gallery при первом запуске (bulk scan) с прогрессом.
[x] Гибридный поиск: фильтр по тегу через `idx_image_tags_tag` (раздел 6.Д).

**Тесты:** эмуляция добавления файлов во время поиска (concurrency), idempotency (повторный скан не дублирует).

---

## Этап 6. Wails API слой (`internal/api`)

[x] `App` struct с методами, экспортируемыми в фронтенд:
  - `Search(query, provider, modelID, K, tagFilter)` → `[{id, path, thumbnailURI, score}]`.
  - `AddModel(cfg)` — регистрация модели по API (раздел 5).
  - `ListModels()`, `ScanFolder(path)`, `GetThumbnails(ids)`.
[x] Отдача превью: метод возвращает data-URI base64 (256×256 webp) или монтирует статику (раздел 6.Б, критика №6).
[x] Подписка на события прогресса сканирования (`wails.Events`).

**Тесты:** вызовы методов через fake frontend context, корректность возвращаемых структур.

---

## Этап 7. Фронтенд (Vue/Svelte) + интеграция

[x] Минимальный UI: поле поиска, галерея превью (только thumbnail-URI), фильтр по тегам.
[x] Панель управления моделями (добавить remote-модель по API: URL + key + model).
[x] Индикатор прогресса сканирования.
[x] Выбор активного `(provider, model_id)` для поиска.

**Критерий:** end-to-end поиск "Cyberpunk city" возвращает релевантные превью < 100 мс (локально).

---

## Этап 8. Сборка, производительность, полировка

[x] Cross-platform build (Windows/macOS/Linux) — проверить, что CGO только в локальном бинаре.
[x] Бенчмарк boot: 100k векторов загружаются параллельно (раздел 3).
[x] Проверка RAM-бюджета (раздел 7) под нагрузкой нескольких моделей.
- [ ] Lint + typecheck + полный прогон тестов (`go test ./...`).
- [ ] Документация запуска в README.

---

## Порядок зависимостей (кратко)

```
Этап 0 (init)
   └─ Этап 1 (db)          ← фундамент персистентности
        └─ Этап 2 (vector) ← ядро поиска, не зависит от инференса
             ├─ Этап 3 (embedder) ← local + remote
             │     └─ Этап 4 (models) ← registry + eviction
             │           └─ Этап 5 (scan) ← использует 1+2+3+4
             │                 └─ Этап 6 (api) ← Wails-граница
             │                       └─ Этап 7 (frontend)
             └─ Этап 8 (polish) ← после сквозного цикла
```

**Важно:** Этапы 1→2 можно завершить и протестировать **полностью на синтетических векторах**, не дожидаясь инференса. Это снижает риск: поисковое ядро стабилизируется до появления CGO/сети.
