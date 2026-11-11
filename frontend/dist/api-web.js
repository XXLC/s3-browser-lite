(function () {
  'use strict';

  function createError(code, message) {
    return Object.assign(new Error(message), { code: code || 'Unknown' });
  }

  function normalizeError(err) {
    if (err && typeof err === 'object' && typeof err.code === 'string' && typeof err.message === 'string') {
      return createError(err.code, err.message);
    }

    const raw = typeof err === 'string'
      ? err
      : (err && typeof err.message === 'string' ? err.message : String(err ?? 'Đã xảy ra lỗi không xác định.'));

    try {
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed === 'object') {
        const message = typeof parsed.message === 'string' && parsed.message.length > 0
          ? parsed.message
          : raw;
        const code = typeof parsed.code === 'string' && parsed.code.length > 0
          ? parsed.code
          : 'Unknown';
        return createError(code, message);
      }
    } catch (_ignore) {
      // Không phải chuỗi JSON, giữ nguyên thông điệp gốc.
    }

    return createError('Unknown', raw);
  }

  function reloadOnUnauthorized(status) {
    if (status === 401 && typeof window !== 'undefined' && window.location && typeof window.location.reload === 'function') {
      window.location.reload();
    }
  }

  async function readPayload(response) {
    if (typeof response.text === 'function') {
      const text = await response.text();
      if (!text) return null;
      try {
        return JSON.parse(text);
      } catch (_ignore) {
        return text;
      }
    }
    if (typeof response.json === 'function') {
      return await response.json();
    }
    return null;
  }

  async function call(method, ...args) {
    let response;
    try {
      response = await fetch(`/api/rpc/${encodeURIComponent(method)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(args),
      });
    } catch (_err) {
      throw createError('NetworkError', 'Mất kết nối tới máy chủ.');
    }

    const payload = await readPayload(response);
    if (!response.ok) {
      reloadOnUnauthorized(response.status);
      if (payload && typeof payload === 'object') {
        throw createError(payload.code || 'Unknown', payload.message || `Lỗi máy chủ (${response.status}).`);
      }
      if (typeof payload === 'string' && payload.length > 0) {
        throw normalizeError(payload);
      }
      throw createError('Unknown', `Lỗi máy chủ (${response.status}).`);
    }

    return payload && typeof payload === 'object' ? payload.result : undefined;
  }

  function normalizeWebPath(raw) {
    return String(raw || '').replace(/\\/g, '/').replace(/^\/+/, '');
  }

  const sizeListeners = new Set();
  const transferListeners = new Set();
  let eventSource = null;
  let hadEventError = false;

  function emitSizeUpdate(update) {
    for (const listener of [...sizeListeners]) {
      listener(update);
    }
  }

  function emitTransferUpdate(job) {
    for (const listener of [...transferListeners]) {
      listener(job);
    }
  }

  function ensureEventSource() {
    if (eventSource) return;
    if (typeof EventSource !== 'function') {
      throw createError('RuntimeNotReady', 'Trình duyệt không hỗ trợ EventSource.');
    }
    const es = new EventSource('/api/events');
    eventSource = es;

    const onSizeEvent = event => {
      if (!event || !event.data) return;
      try {
        emitSizeUpdate(JSON.parse(event.data));
      } catch (_ignore) {}
    };

    const onTransferEvent = event => {
      if (!event || !event.data) return;
      try {
        emitTransferUpdate(JSON.parse(event.data));
      } catch (_ignore) {}
    };

    const onError = () => {
      if (eventSource !== es) return;
      hadEventError = true;
    };

    // SSE không phát lại sự kiện cũ; nối lại sau lỗi cần đồng bộ lại danh sách tác vụ.
    const onOpen = () => {
      if (eventSource !== es || !hadEventError) return;
      hadEventError = false;
      call('TransfersList').then(jobs => {
        if (Array.isArray(jobs)) {
          for (const job of jobs) emitTransferUpdate(job);
        }
      }).catch(() => {});
    };

    if (typeof es.addEventListener === 'function') {
      es.addEventListener('size:update', onSizeEvent);
      es.addEventListener('transfers:update', onTransferEvent);
      es.addEventListener('error', onError);
      es.addEventListener('open', onOpen);
    }
    es.onerror = onError;
    es.onopen = onOpen;
  }

  function closeEventSourceIfIdle() {
    if (sizeListeners.size === 0 && transferListeners.size === 0 && eventSource) {
      if (typeof eventSource.close === 'function') {
        eventSource.close();
      }
      eventSource = null;
      hadEventError = false;
    }
  }

  let nextWebUploadId = 0;
  const activeUploads = new Map();

  function uploadJobName(items) {
    const roots = [];
    for (const item of items) {
      const rawPath = normalizeWebPath((item && item.path) || (item && item.file && (item.file.webkitRelativePath || item.file.name)) || '');
      const root = rawPath.split('/').filter(Boolean)[0];
      if (root && !roots.includes(root)) roots.push(root);
    }
    if (roots.length === 0) return 'tệp';
    if (roots.length === 1) return roots[0];
    return `${roots[0]} (+${roots.length - 1})`;
  }

  function uploadViaXHR(id, bucket, prefix, items) {
    if (!Array.isArray(items) || items.length === 0) {
      return Promise.reject(createError('InvalidArguments', 'Không có tệp để tải lên.'));
    }

    const jobId = `web-upload-${++nextWebUploadId}`;
    let totalBytes = 0;
    const form = new FormData();

    for (const item of items) {
      const file = item && item.file ? item.file : item;
      const relPath = normalizeWebPath((item && item.path) || (file && (file.webkitRelativePath || file.name)) || '');
      if (file && typeof file.size === 'number' && file.size > 0) {
        totalBytes += file.size;
      }
      form.append('path', relPath);
      if (typeof Blob !== 'undefined' && file instanceof Blob) {
        form.append('file', file, file.name || 'file');
      } else {
        form.append('file', file);
      }
    }

    const job = {
      id: jobId,
      type: 'upload',
      name: uploadJobName(items),
      status: 'running',
      transferred: 0,
      total: totalBytes,
      speed: 0,
      error: null,
    };

    const params = new URLSearchParams({
      profile: id == null ? '' : String(id),
      bucket: bucket == null ? '' : String(bucket),
      prefix: prefix == null ? '' : String(prefix),
    });

    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      const startedAt = Date.now();
      let settled = false;

      const entry = {
        xhr,
        job,
        cancel() {
          if (settled) return;
          settled = true;
          activeUploads.delete(jobId);
          if (typeof xhr.abort === 'function') {
            xhr.abort();
          }
          job.status = 'cancelled';
          job.speed = 0;
          job.error = null;
          emitTransferUpdate({ ...job });
          resolve([]);
        },
      };

      activeUploads.set(jobId, entry);
      emitTransferUpdate({ ...job });

      if (xhr.upload) {
        let lastProgressEvent = null;
        const onProgress = event => {
          if (settled || !event || event === lastProgressEvent) return;
          lastProgressEvent = event;
          if (typeof event.loaded === 'number') {
            job.transferred = event.loaded;
          }
          if (event.lengthComputable && typeof event.total === 'number' && event.total > 0) {
            job.total = event.total;
          }
          const elapsedSec = (Date.now() - startedAt) / 1000;
          job.speed = elapsedSec > 0 ? Math.round(job.transferred / elapsedSec) : 0;
          emitTransferUpdate({ ...job });
        };
        xhr.upload.onprogress = onProgress;
        if (typeof xhr.upload.addEventListener === 'function') {
          xhr.upload.addEventListener('progress', onProgress);
        }
      }

      const onLoad = () => {
        if (settled) return;
        settled = true;
        activeUploads.delete(jobId);

        if (xhr.status >= 200 && xhr.status < 300) {
          let result = [];
          if (xhr.responseText) {
            try {
              const parsed = JSON.parse(xhr.responseText);
              if (parsed && Array.isArray(parsed.result)) {
                result = parsed.result;
              }
            } catch (_ignore) {}
          }
          emitTransferUpdate({ ...job, removed: true });
          resolve(result);
          return;
        }

        reloadOnUnauthorized(xhr.status);
        const err = xhr.responseText
          ? normalizeError(xhr.responseText)
          : createError('Unknown', `Lỗi tải lên (${xhr.status}).`);
        job.status = 'failed';
        job.speed = 0;
        job.error = { code: err.code, message: err.message };
        emitTransferUpdate({ ...job });
        reject(err);
      };

      const onError = () => {
        if (settled) return;
        settled = true;
        activeUploads.delete(jobId);
        const err = createError('NetworkError', 'Mất kết nối tới máy chủ khi tải lên.');
        job.status = 'failed';
        job.speed = 0;
        job.error = { code: err.code, message: err.message };
        emitTransferUpdate({ ...job });
        reject(err);
      };

      const onAbort = () => {
        if (settled) return;
        settled = true;
        activeUploads.delete(jobId);
        job.status = 'cancelled';
        job.speed = 0;
        job.error = null;
        emitTransferUpdate({ ...job });
        resolve([]);
      };

      xhr.onload = onLoad;
      xhr.onerror = onError;
      xhr.onabort = onAbort;
      if (typeof xhr.addEventListener === 'function') {
        xhr.addEventListener('load', onLoad);
        xhr.addEventListener('error', onError);
        xhr.addEventListener('abort', onAbort);
      }

      xhr.open('POST', `/api/upload?${params.toString()}`, true);
      xhr.withCredentials = true;
      xhr.send(form);
    });
  }

  function selectUploadFiles(directory) {
    return new Promise(resolve => {
      const input = document.createElement('input');
      input.type = 'file';
      input.multiple = true;
      if (directory) {
        input.webkitdirectory = true;
        if (typeof input.setAttribute === 'function') {
          input.setAttribute('webkitdirectory', '');
        }
      }
      input.hidden = true;
      if (document.body && typeof document.body.appendChild === 'function') {
        document.body.appendChild(input);
      }

      let settled = false;
      const finish = items => {
        if (settled) return;
        settled = true;
        if (typeof input.remove === 'function') input.remove();
        resolve(items);
      };

      const onChange = () => {
        const files = Array.from(input.files || []);
        const items = files.map(file => ({
          file,
          path: normalizeWebPath((directory && file.webkitRelativePath) || file.webkitRelativePath || file.name || ''),
        })).filter(item => item.path.length > 0);
        finish(items);
      };

      const onCancel = () => finish([]);

      if (typeof input.addEventListener === 'function') {
        input.addEventListener('change', onChange, { once: true });
        input.addEventListener('cancel', onCancel, { once: true });
      } else {
        input.onchange = onChange;
        input.oncancel = onCancel;
      }

      if (typeof input.click === 'function') {
        input.click();
      }
    });
  }

  async function downloadViaForm(id, bucket, keys) {
    let iframe = document.getElementById('s3bDownloadFrame');
    if (!iframe) {
      iframe = document.createElement('iframe');
      iframe.id = 's3bDownloadFrame';
      iframe.name = 's3bDownloadFrame';
      iframe.hidden = true;
      document.body.appendChild(iframe);
    }
    const form = document.createElement('form');
    form.method = 'POST';
    form.action = '/api/download';
    form.target = 's3bDownloadFrame';
    form.hidden = true;

    const fields = {
      profile: id == null ? '' : String(id),
      bucket: bucket == null ? '' : String(bucket),
      keys: JSON.stringify(Array.isArray(keys) ? keys : []),
    };
    for (const [name, value] of Object.entries(fields)) {
      const input = document.createElement('input');
      input.type = 'hidden';
      input.name = name;
      input.value = value;
      form.appendChild(input);
    }
    document.body.appendChild(form);
    form.submit();
    if (typeof form.remove === 'function') {
      form.remove();
    }
    return [];
  }

  function readFileFromEntry(entry, path) {
    return new Promise(resolve => {
      if (typeof entry.file !== 'function') {
        resolve([]);
        return;
      }
      entry.file(
        file => resolve(file ? [{ file, path: normalizeWebPath(path || file.name) }] : []),
        () => resolve([])
      );
    });
  }

  function readAllDirectoryEntries(reader) {
    return new Promise(resolve => {
      if (!reader || typeof reader.readEntries !== 'function') {
        resolve([]);
        return;
      }
      const all = [];
      const seen = new Set();
      // readEntries của trình duyệt có thể chỉ trả từng lô nhỏ nên cần đọc lặp tới khi rỗng.
      const nextBatch = () => {
        reader.readEntries(
          batch => {
            if (!Array.isArray(batch) || batch.length === 0) {
              resolve(all);
              return;
            }
            let added = 0;
            for (const entry of batch) {
              if (!entry) continue;
              const key = entry.fullPath || `${entry.isDirectory ? 'd' : 'f'}:${entry.name}`;
              if (!seen.has(key)) {
                seen.add(key);
                all.push(entry);
                added++;
              }
            }
            if (added === 0) {
              resolve(all);
              return;
            }
            nextBatch();
          },
          () => resolve(all)
        );
      };
      nextBatch();
    });
  }

  async function traverseEntry(entry, parentPath) {
    if (!entry) return [];
    const name = normalizeWebPath(entry.name || '');
    const currentPath = parentPath ? (name ? `${parentPath}/${name}` : parentPath) : name;
    if (entry.isFile) {
      return await readFileFromEntry(entry, currentPath);
    }
    if (entry.isDirectory && typeof entry.createReader === 'function') {
      const children = await readAllDirectoryEntries(entry.createReader());
      const nested = [];
      for (const child of children) {
        const items = await traverseEntry(child, currentPath);
        nested.push(...items);
      }
      return nested;
    }
    return [];
  }

  async function writeClipboardText(value) {
    const text = value == null ? '' : String(value);
    if (typeof navigator !== 'undefined' && navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      try {
        await navigator.clipboard.writeText(text);
        return;
      } catch (_ignore) {
        // Chuyển sang cơ chế dự phòng bằng textarea khi ngữ cảnh không cho phép Clipboard API.
      }
    }

    if (typeof document === 'undefined' || !document.body || typeof document.execCommand !== 'function') {
      throw createError('ClipboardError', 'Trình duyệt không hỗ trợ sao chép vào bộ nhớ tạm.');
    }

    const container = (typeof document.querySelector === 'function' && document.querySelector('dialog[open]')) || document.body;
    const textarea = document.createElement('textarea');
    textarea.value = text;
    if (typeof textarea.setAttribute === 'function') {
      textarea.setAttribute('readonly', '');
    }
    if (textarea.style) {
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      textarea.style.pointerEvents = 'none';
    }
    container.appendChild(textarea);
    try {
      if (typeof textarea.focus === 'function') textarea.focus();
      if (typeof textarea.select === 'function') textarea.select();
      const copied = document.execCommand('copy');
      if (!copied) {
        throw createError('ClipboardError', 'Không thể sao chép vào bộ nhớ tạm.');
      }
    } catch (err) {
      throw normalizeError(err && err.code ? err : createError('ClipboardError', 'Không thể sao chép vào bộ nhớ tạm.'));
    } finally {
      if (typeof textarea.remove === 'function') {
        textarea.remove();
      }
    }
  }

  window.api = {
    profiles: {
      list: () => call('ProfilesList'),
      save: profile => call('ProfilesSave', profile),
      remove: id => call('ProfilesRemove', id),
      duplicate: id => call('ProfilesDuplicate', id),
      test: profileOrId => (
        typeof profileOrId === 'string'
          ? call('ProfilesTestID', profileOrId)
          : call('ProfilesTestDraft', profileOrId)
      ),
      import: json => call('ProfilesImport', json),
      export: () => call('ProfilesExport'),
    },
    s3: {
      listBuckets: id => call('S3ListBuckets', id),
      createBucket: (id, name) => call('S3CreateBucket', id, name),
      list: (id, bucket, prefix, token) => call(
        'S3List',
        id,
        bucket,
        prefix == null ? '' : prefix,
        token == null ? '' : token
      ),
      head: (id, bucket, key) => call('S3Head', id, bucket, key),
      delete: (id, bucket, keys) => call('S3Delete', id, bucket, keys),
      deletePrefix: (id, bucket, prefix) => call('S3DeletePrefix', id, bucket, prefix),
      createFolder: (id, bucket, prefix) => call('S3CreateFolder', id, bucket, prefix),
      move: (id, bucket, source, target) => call('S3Move', id, bucket, source, target),
      presign: (id, bucket, key, expiresSec) => call('S3Presign', id, bucket, key, expiresSec),
      getBucketPolicy: (id, bucket) => call('S3GetBucketPolicy', id, bucket),
      putBucketPolicy: (id, bucket, policy) => call('S3PutBucketPolicy', id, bucket, policy),
      deleteBucketPolicy: (id, bucket) => call('S3DeleteBucketPolicy', id, bucket),
      sizeStart: (id, bucket, prefix, force) => call('S3SizeStart', id, bucket, prefix || '', Boolean(force)),
      sizeCancel: (id, bucket, prefix) => call('S3SizeCancel', id, bucket, prefix || ''),
      onSize: callback => {
        if (typeof callback !== 'function') {
          throw new Error('Hàm nhận dung lượng không hợp lệ.');
        }
        ensureEventSource();
        sizeListeners.add(callback);
        return () => {
          sizeListeners.delete(callback);
          closeEventSourceIfIdle();
        };
      },
    },
    transfers: {
      upload: (id, bucket, prefix, items) => uploadViaXHR(id, bucket, prefix == null ? '' : prefix, items),
      download: (id, bucket, keys, _directory) => downloadViaForm(id, bucket, keys),
      copy: (source, sourceBucket, keys, target, targetBucket, prefix) => call(
        'TransfersCopy',
        source,
        sourceBucket,
        keys,
        target,
        targetBucket,
        prefix == null ? '' : prefix
      ),
      cancel: id => {
        if (typeof id === 'string' && id.startsWith('web-upload-')) {
          const entry = activeUploads.get(id);
          if (entry) entry.cancel();
          return Promise.resolve();
        }
        return call('TransfersCancel', id);
      },
      retry: id => {
        if (typeof id === 'string' && id.startsWith('web-upload-')) {
          return Promise.reject(createError('Unsupported', 'Không thể thử lại tác vụ tải lên từ trình duyệt. Vui lòng chọn lại tệp.'));
        }
        return call('TransfersRetry', id);
      },
      list: () => call('TransfersList'),
      setConcurrency: count => call('TransfersSetConcurrency', count),
      onUpdate: callback => {
        if (typeof callback !== 'function') {
          throw new Error('Hàm nhận tiến độ không hợp lệ.');
        }
        ensureEventSource();
        transferListeners.add(callback);
        return () => {
          transferListeners.delete(callback);
          closeEventSourceIfIdle();
        };
      },
    },
    dialogs: {
      upload: directory => selectUploadFiles(Boolean(directory)),
      downloadDirectory: () => Promise.resolve('browser'),
    },
    files: {
      onDrop: callback => {
        if (typeof callback !== 'function') {
          throw new Error('Hàm nhận tệp kéo thả không hợp lệ.');
        }
        let active = true;
        // Đọc webkitGetAsEntry đồng bộ ngay trong sự kiện drop trước khi trình duyệt xóa DataTransferItemList.
        const handler = event => {
          if (!active || !event) return;
          if (typeof event.preventDefault === 'function') {
            event.preventDefault();
          }
          const dt = event.dataTransfer;
          const entries = [];
          if (dt && dt.items) {
            for (const item of Array.from(dt.items)) {
              if (item && typeof item.webkitGetAsEntry === 'function') {
                const entry = item.webkitGetAsEntry();
                if (entry) entries.push(entry);
              }
            }
          }
          const fallbackFiles = entries.length === 0 && dt && dt.files
            ? Array.from(dt.files)
            : [];

          (async () => {
            let items = [];
            if (entries.length > 0) {
              for (const entry of entries) {
                const collected = await traverseEntry(entry, '');
                items.push(...collected);
              }
            } else if (fallbackFiles.length > 0) {
              items = fallbackFiles.map(file => ({
                file,
                path: normalizeWebPath(file.webkitRelativePath || file.name || ''),
              })).filter(item => item.path.length > 0);
            }
            if (active) {
              callback(items);
            }
          })();
        };

        document.addEventListener('drop', handler);
        return () => {
          active = false;
          document.removeEventListener('drop', handler);
        };
      },
    },
    clipboard: {
      writeText: value => writeClipboardText(value),
    },
  };
})();
