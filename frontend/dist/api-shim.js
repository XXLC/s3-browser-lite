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

  function getApp() {
    const app = window.go && window.go.main && window.go.main.App;
    if (!app) {
      throw createError('RuntimeNotReady', 'Cầu nối Go (window.go.main.App) chưa sẵn sàng.');
    }
    return app;
  }

  function getRuntime() {
    if (!window.runtime) {
      throw createError('RuntimeNotReady', 'Wails runtime (window.runtime) chưa sẵn sàng.');
    }
    return window.runtime;
  }

  async function call(method, ...args) {
    try {
      const app = getApp();
      if (typeof app[method] !== 'function') {
        throw createError('MethodNotFound', `Phương thức Go "${method}" không tồn tại.`);
      }
      return await app[method](...args);
    } catch (err) {
      throw normalizeError(err);
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
        const rt = getRuntime();
        if (typeof rt.EventsOn !== 'function') {
          throw createError('RuntimeNotReady', 'Wails runtime chưa hỗ trợ đăng ký sự kiện.');
        }
        const off = rt.EventsOn('size:update', update => {
          callback(update);
        });
        return () => {
          if (typeof off === 'function') {
            off();
          } else if (typeof rt.EventsOff === 'function') {
            rt.EventsOff('size:update');
          }
        };
      },
    },
    transfers: {
      upload: (id, bucket, prefix, paths) => call(
        'TransfersUpload',
        id,
        bucket,
        prefix == null ? '' : prefix,
        paths
      ),
      download: (id, bucket, keys, directory) => call('TransfersDownload', id, bucket, keys, directory),
      copy: (source, sourceBucket, keys, target, targetBucket, prefix) => call(
        'TransfersCopy',
        source,
        sourceBucket,
        keys,
        target,
        targetBucket,
        prefix == null ? '' : prefix
      ),
      cancel: id => call('TransfersCancel', id),
      retry: id => call('TransfersRetry', id),
      list: () => call('TransfersList'),
      setConcurrency: count => call('TransfersSetConcurrency', count),
      onUpdate: callback => {
        if (typeof callback !== 'function') {
          throw new Error('Hàm nhận tiến độ không hợp lệ.');
        }
        const rt = getRuntime();
        if (typeof rt.EventsOn !== 'function') {
          throw createError('RuntimeNotReady', 'Wails runtime chưa hỗ trợ đăng ký sự kiện.');
        }
        const off = rt.EventsOn('transfers:update', update => {
          callback(update);
        });
        return () => {
          if (typeof off === 'function') {
            off();
          } else if (typeof rt.EventsOff === 'function') {
            rt.EventsOff('transfers:update');
          }
        };
      },
    },
    dialogs: {
      upload: directory => call('DialogsUpload', Boolean(directory)),
      downloadDirectory: () => call('DialogsDownloadDirectory'),
    },
    files: {
      onDrop: callback => {
        if (typeof callback !== 'function') {
          throw new Error('Hàm nhận tệp kéo thả không hợp lệ.');
        }
        const rt = getRuntime();
        if (typeof rt.OnFileDrop !== 'function') {
          throw createError('RuntimeNotReady', 'Wails runtime chưa hỗ trợ kéo thả tệp.');
        }
        let active = true;
        const off = rt.OnFileDrop((_x, _y, paths) => {
          if (active && Array.isArray(paths)) {
            callback(paths);
          }
        }, true);
        return () => {
          active = false;
          if (typeof off === 'function') {
            off();
          } else if (typeof rt.OnFileDropOff === 'function') {
            rt.OnFileDropOff();
          }
        };
      },
    },
    clipboard: {
      writeText: value => call('ClipboardWriteText', value == null ? '' : String(value)),
    },
  };
})();
