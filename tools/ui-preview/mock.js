(function () {
  'use strict';

  const profiles = [
    {
      id: 'prof-ecs-prod',
      name: 'ECS-Production',
      endpoint: 'https://s3-ecs.internal.local',
      region: 'us-east-1',
      accessKeyId: 'DEMO_ACCESS_01',
      hasSecret: true,
      forcePathStyle: true,
      allowInsecureTls: false,
      defaultBucket: 'backup-primary',
    },
    {
      id: 'prof-minio-dr',
      name: 'MinIO-DR-Backup',
      endpoint: 'https://minio-dr.internal.local',
      region: 'ap-southeast-1',
      accessKeyId: 'DEMO_ACCESS_02',
      hasSecret: true,
      forcePathStyle: true,
      allowInsecureTls: false,
      defaultBucket: 'backup-app-v2',
    },
    {
      id: 'prof-ceph-archive',
      name: 'Ceph-Archive-Storage',
      endpoint: 'https://ceph-s3.internal.local',
      region: 'us-east-1',
      accessKeyId: 'DEMO_ACCESS_03',
      hasSecret: true,
      forcePathStyle: true,
      allowInsecureTls: false,
      defaultBucket: '',
    },
  ];

  const buckets = [
    { name: 'backup-primary' },
    { name: 'backup-app-v2' },
    { name: 'backup-db-daily' },
  ];

  const bucketPolicies = {
    'backup-primary': JSON.stringify({
      Version: '2012-10-17',
      Statement: [
        {
          Sid: 'AllowReadBackupObjects',
          Effect: 'Allow',
          Principal: { AWS: ['arn:aws:iam::123456789012:role/his-backup-reader'] },
          Action: ['s3:GetObject', 's3:ListBucket'],
          Resource: [
            'arn:aws:s3:::backup-primary',
            'arn:aws:s3:::backup-primary/*',
          ],
        },
      ],
    }, null, 2),
  };

  const directoryData = {
    '': {
      folders: [
        { prefix: '_backup_state/' },
        { prefix: '2026-10-25/' },
        { prefix: '2026-10-26/' },
        { prefix: '2026-10-27/' },
        { prefix: '2026-10-28/' },
        { prefix: '2026-10-29/' },
        { prefix: '2026-10-30/' },
        { prefix: '2026-10-31/' },
        { prefix: '2026-11-01/' },
        { prefix: '2026-11-02/' },
        { prefix: '2026-11-03/' },
        { prefix: '2026-11-04/' },
      ],
      objects: [
        { key: 'backup-manifest.json', size: 18432, lastModified: '2026-11-04T07:15:00Z' },
        { key: 'latest-checkpoint.sha256', size: 512, lastModified: '2026-11-04T07:16:12Z' },
        { key: 'retention-policy.yaml', size: 4210, lastModified: '2026-10-01T02:00:00Z' },
        { key: 'huong-dan-khoi-phuc.txt', size: 9640, lastModified: '2026-10-15T09:30:00Z' },
      ],
    },
    '_backup_state/': {
      folders: [
        { prefix: '_backup_state/full/' },
        { prefix: '_backup_state/incremental/' },
        { prefix: '_backup_state/wal-archive/' },
      ],
      objects: [
        { key: '_backup_state/state-index.json', size: 65536, lastModified: '2026-11-04T06:00:00Z' },
      ],
    },
    '_backup_state/full/': {
      folders: [
        { prefix: '_backup_state/full/node-app-01/' },
        { prefix: '_backup_state/full/node-app-02/' },
        { prefix: '_backup_state/full/node-db-01/' },
        { prefix: '_backup_state/full/node-db-02/' },
        { prefix: '_backup_state/full/node-01/' },
        { prefix: '_backup_state/full/node-02/' },
        { prefix: '_backup_state/full/node-gateway/' },
        { prefix: '_backup_state/full/node-his-core/' },
        { prefix: '_backup_state/full/node-lis-01/' },
        { prefix: '_backup_state/full/node-pacs-01/' },
        { prefix: '_backup_state/full/node-pacs-02/' },
        { prefix: '_backup_state/full/node-ris-01/' },
      ],
      objects: [
        { key: '_backup_state/full/full-backup-20261104.tar.zst', size: 4823449600, lastModified: '2026-11-04T05:30:00Z' },
        { key: '_backup_state/full/metadata-inventory.csv', size: 1458200, lastModified: '2026-11-04T05:32:10Z' },
        { key: '_backup_state/full/schema-dump-v42.sql.gz', size: 89451200, lastModified: '2026-11-04T05:12:45Z' },
        { key: '_backup_state/full/verify-report.txt', size: 15840, lastModified: '2026-11-04T05:40:00Z' },
      ],
    },
  };

  const sizeTable = {
    '': { bytes: 518181093376, objects: 142850 },
    '_backup_state/': { bytes: 68719476736, objects: 19420 },
    '_backup_state/full/': { bytes: 41231686041, objects: 12460 },
    '_backup_state/full/node-app-01/': { bytes: 2469606195, objects: 1420 },
    '_backup_state/full/node-app-02/': { bytes: 2362232012, objects: 1385 },
    '_backup_state/full/node-db-01/': { bytes: 12670153523, objects: 640 },
    '_backup_state/full/node-db-02/': { bytes: 11918534246, objects: 615 },
    '_backup_state/full/node-01/': { bytes: 3865470566, objects: 2890 },
    '_backup_state/full/node-02/': { bytes: 3543348019, objects: 2710 },
    '_backup_state/full/node-gateway/': { bytes: 412316860, objects: 320 },
    '_backup_state/full/node-his-core/': { bytes: 1932735283, objects: 1150 },
    '_backup_state/full/node-lis-01/': { bytes: 845120400, objects: 540 },
    '_backup_state/full/node-pacs-01/': { bytes: 5905580032, objects: 4120 },
    '_backup_state/full/node-pacs-02/': { bytes: 5476083302, objects: 3890 },
    '_backup_state/full/node-ris-01/': { bytes: 734003200, objects: 480 },
  };

  function getTargetSize(prefix) {
    if (sizeTable[prefix]) return sizeTable[prefix];
    let hash = 0;
    for (let i = 0; i < prefix.length; i++) hash = (hash * 31 + prefix.charCodeAt(i)) >>> 0;
    return {
      bytes: ((hash % 9000) + 250) * 1024 * 1024,
      objects: (hash % 4500) + 80,
    };
  }

  const sizeListeners = new Set();
  const transferListeners = new Set();
  const activeTimers = new Map();

  function emitSize(payload) {
    for (const fn of sizeListeners) {
      try { fn(payload); } catch (_ignore) {}
    }
  }

  function timerKey(id, bucket, prefix) {
    return `${id}::${bucket}::${prefix}`;
  }

  function clearJobTimers(key) {
    const timers = activeTimers.get(key);
    if (timers) {
      for (const timer of timers) clearTimeout(timer);
      activeTimers.delete(key);
      return true;
    }
    return false;
  }

  window.api = {
    profiles: {
      list: async () => profiles.map(item => ({ ...item })),
      save: async profile => ({ ...profile, id: profile.id || 'prof-new', hasSecret: true }),
      remove: async () => true,
      duplicate: async id => ({ ...profiles[0], id: `${id}-copy`, name: `${profiles[0].name} (Bản sao)` }),
      test: async () => true,
      import: async () => profiles.length,
      export: async () => JSON.stringify(profiles, null, 2),
    },
    s3: {
      listBuckets: async () => buckets.map(item => ({ ...item })),
      createBucket: async (_id, name) => ({ name }),
      list: async (_id, _bucket, prefix) => {
        const key = prefix || '';
        const found = directoryData[key] || { folders: [], objects: [] };
        return {
          folders: found.folders.map(item => ({ ...item })),
          objects: found.objects.map(item => ({ ...item })),
          nextToken: null,
        };
      },
      head: async (_id, _bucket, key) => ({
        size: 1048576,
        etag: '"9b2cf535f27731c974343645a3985328"',
        contentType: 'application/octet-stream',
        lastModified: '2026-11-04T07:15:00Z',
        metadata: { environment: 'production' },
        key,
      }),
      delete: async () => true,
      deletePrefix: async () => true,
      createFolder: async () => true,
      move: async () => true,
      presign: async (_id, bucket, key) => `https://s3-ecs.internal.local/${bucket}/${key}?X-Amz-Expires=3600`,
      getBucketPolicy: async (_id, bucket) => bucketPolicies[bucket] || '',
      putBucketPolicy: async (_id, bucket, policy) => {
        bucketPolicies[bucket] = JSON.stringify(JSON.parse(policy), null, 2);
        return true;
      },
      deleteBucketPolicy: async (_id, bucket) => {
        delete bucketPolicies[bucket];
        return true;
      },
      sizeStart: async (id, bucket, prefix) => {
        const normPrefix = prefix || '';
        const key = timerKey(id, bucket, normPrefix);
        clearJobTimers(key);
        const target = getTargetSize(normPrefix);
        const t1 = setTimeout(() => {
          emitSize({
            profileId: id,
            bucket,
            prefix: normPrefix,
            state: 'queued',
            bytes: 0,
            objects: 0,
            message: '',
          });
        }, 20);
        const t2 = setTimeout(() => {
          emitSize({
            profileId: id,
            bucket,
            prefix: normPrefix,
            state: 'running',
            bytes: Math.floor(target.bytes * 0.45),
            objects: Math.floor(target.objects * 0.45),
            message: '',
          });
        }, 120);
        const t3 = setTimeout(() => {
          activeTimers.delete(key);
          emitSize({
            profileId: id,
            bucket,
            prefix: normPrefix,
            state: 'done',
            bytes: target.bytes,
            objects: target.objects,
            message: '',
          });
        }, 320);
        activeTimers.set(key, [t1, t2, t3]);
        return true;
      },
      sizeCancel: async (id, bucket, prefix) => {
        const normPrefix = prefix || '';
        const key = timerKey(id, bucket, normPrefix);
        const had = clearJobTimers(key);
        if (had) {
          emitSize({
            profileId: id,
            bucket,
            prefix: normPrefix,
            state: 'cancelled',
            bytes: 0,
            objects: 0,
            message: '',
          });
        }
        return had;
      },
      onSize: callback => {
        sizeListeners.add(callback);
        return () => sizeListeners.delete(callback);
      },
    },
    transfers: {
      upload: async () => true,
      download: async () => true,
      copy: async () => true,
      cancel: async () => true,
      retry: async () => true,
      list: async () => [
        {
          id: 'tr-1',
          type: 'download',
          name: 'full-backup-20261104.tar.zst',
          transferred: 2894069760,
          total: 4823449600,
          speed: 125829120,
          status: 'running',
        },
      ],
      setConcurrency: async () => true,
      onUpdate: callback => {
        transferListeners.add(callback);
        return () => transferListeners.delete(callback);
      },
    },
    dialogs: {
      upload: async () => [],
      downloadDirectory: async () => '',
    },
    files: {
      onDrop: () => () => {},
    },
    clipboard: {
      writeText: async () => true,
    },
  };

  // Tự động mở hồ sơ đầu tiên và vào thư mục con để hiển thị đầy đủ bố cục xem thử.
  function triggerPreviewModal() {
    const searchMode = new URLSearchParams(window.location.search).get('open');
    const hashMode = new URLSearchParams(window.location.hash.replace(/^#/, '')).get('open');
    const mode = searchMode || hashMode;
    if (!mode) return;
    if (mode === 'add-storage' || mode === 'profile') {
      document.getElementById('addProfile')?.click();
    } else if (mode === 'policy') {
      document.getElementById('bucketPolicy')?.click();
    } else if (mode === 'policy-empty') {
      const secondBucket = Array.from(document.querySelectorAll('#buckets button'))
        .find(btn => btn.textContent === 'backup-app-v2');
      if (secondBucket) {
        secondBucket.click();
        setTimeout(() => document.getElementById('bucketPolicy')?.click(), 120);
      } else {
        delete bucketPolicies['backup-primary'];
        document.getElementById('bucketPolicy')?.click();
      }
    } else if (mode === 'arn') {
      document.getElementById('bucketArn')?.click();
    }
  }

  function autoOpenPreview() {
    const openBtn = document.getElementById('openProfile');
    if (!openBtn) return;
    openBtn.click();
    setTimeout(() => {
      const firstFolder = document.querySelector('#objects .object-link');
      if (firstFolder && firstFolder.textContent === '_backup_state/') {
        firstFolder.click();
        setTimeout(() => {
          const subFolder = document.querySelector('#objects .object-link');
          if (subFolder && subFolder.textContent === 'full/') {
            subFolder.click();
            setTimeout(triggerPreviewModal, 150);
          } else {
            triggerPreviewModal();
          }
        }, 120);
      } else {
        triggerPreviewModal();
      }
    }, 120);
  }

  const observer = new MutationObserver(() => {
    if (document.documentElement.dataset.ready === 'true') {
      observer.disconnect();
      autoOpenPreview();
    }
  });
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-ready'] });

  // Dự phòng khi mở trực tiếp qua giao thức file:// (trình duyệt chặn ES module CORS trên file://).
  window.addEventListener('DOMContentLoaded', () => {
    setTimeout(async () => {
      if (document.documentElement.dataset.ready === 'true') return;
      const t = {
  app: 'S3 Browser Lite', profiles: 'Cấu hình kết nối Storage', storages: 'S3 Storage', buckets: 'Bucket của Storage hiện tại',
  add: 'Thêm Storage', edit: 'Sửa', remove: 'Xóa', duplicate: 'Nhân bản', test: 'Kiểm tra', open: 'Mở tab',
  import: 'Nhập từ JSON', export: 'Xuất JSON', close: 'Đóng', cancel: 'Hủy', save: 'Lưu hồ sơ',
  profileTitle: 'Hồ sơ kết nối', name: 'Tên hồ sơ', endpoint: 'Endpoint (để trống khi dùng AWS)',
  region: 'Vùng', accessKey: 'Mã khóa truy cập', secret: 'Khóa bí mật',
  secretKeep: 'Đã lưu khóa bí mật. Để trống để giữ nguyên.', secretNew: 'Chưa có khóa bí mật. Hồ sơ nhập JSON cần bổ sung khóa trước khi kết nối.',
  pathStyle: 'Dùng kiểu đường dẫn forcePathStyle', insecure: 'Bỏ kiểm tra chứng chỉ TLS cho hồ sơ này',
  insecureWarning: 'Cảnh báo: hồ sơ này bỏ kiểm tra chứng chỉ TLS. Chỉ dùng với endpoint tự ký đáng tin cậy.',
  defaultBucket: 'Bucket mặc định (tùy chọn)', noProfiles: 'Chưa có hồ sơ. Thêm hoặc nhập hồ sơ để bắt đầu.',
  noTab: 'Mở hồ sơ để duyệt dữ liệu', noBucket: 'Chọn bucket ở thanh bên trái.', noObjects: 'Thư mục hiện tại không có mục phù hợp.',
  noBuckets: 'Chưa có bucket hoặc chưa tải danh sách.', noTransfers: 'Chưa có tác vụ truyền tệp.',
  refresh: 'Làm mới', upload: 'Tải tệp lên', uploadFolder: 'Tải thư mục lên', download: 'Tải xuống',
  createFolder: 'Tạo thư mục', move: 'Đổi tên hoặc di chuyển', copy: 'Sao chép sang tab',
  presign: 'Sao chép URL', properties: 'Thuộc tính', filter: 'Lọc tên trong thư mục hiện tại',
  objectName: 'Tên', size: 'Kích thước', modified: 'Ngày sửa', selectAll: 'Chọn tất cả',
  ascending: 'Tăng dần', descending: 'Giảm dần', more: 'Tải thêm', loading: 'Đang tải dữ liệu...',
  dropHint: 'Thả tệp hoặc thư mục vào bảng để tải lên bucket đang mở.',
  transfers: 'Truyền tệp', concurrency: 'Số tác vụ đồng thời', task: 'Tác vụ', progress: 'Tiến độ',
  speed: 'Tốc độ', status: 'Trạng thái', actions: 'Thao tác', retry: 'Thử lại',
  queued: 'Đang chờ', running: 'Đang chạy', cancelling: 'Đang hủy', cancelled: 'Đã hủy', completed: 'Hoàn thành', failed: 'Thất bại',
  uploadType: 'Tải lên', downloadType: 'Tải xuống', copyType: 'Sao chép', folderType: 'Thư mục',
  connectionOk: 'Kiểm tra kết nối thành công.', saved: 'Đã lưu hồ sơ.', removed: 'Đã xóa hồ sơ.',
  duplicated: 'Đã nhân bản hồ sơ, bao gồm khóa bí mật đã mã hóa.', imported: 'Đã nhập hồ sơ. Hãy bổ sung khóa bí mật trước khi kết nối.',
  importTitle: 'Nhập danh sách hồ sơ', json: 'Nội dung JSON', exportTitle: 'Xuất hồ sơ không kèm khóa bí mật',
  copyJson: 'Sao chép JSON', copied: 'Đã sao chép vào bộ nhớ tạm.',
  confirmRemoveProfile: 'Xóa hồ sơ này? Dữ liệu trên S3 không bị xóa.',
  confirmDelete: count => `Xóa vĩnh viễn ${count} mục đã chọn? Thư mục được xóa đệ quy cùng toàn bộ nội dung. Thao tác này không thể hoàn tác.`,
  confirmMove: 'Đối tượng trùng tên tại đích có thể bị ghi đè. Nguồn chỉ bị xóa sau khi sao chép thành công.',
  confirmCopy: 'Đối tượng trùng tên tại đích có thể bị ghi đè. Dữ liệu được truyền qua máy này.',
  createBucket: 'Tạo bucket mới', bucketName: 'Tên bucket (3 đến 63 ký tự: chữ thường, số, dấu chấm, dấu gạch ngang)', bucketCreated: 'Đã tạo bucket.',
  folderName: 'Tên thư mục mới', targetKey: 'Tên hoặc đường dẫn đích tính từ gốc bucket',
  targetTab: 'Tab đích', targetPrefix: 'Thư mục đích tính từ gốc bucket (để trống là gốc)',
  expiry: 'Thời hạn URL (giây, tối đa 604800)', urlWarning: 'Bất kỳ ai có URL đều có thể tải đối tượng trong thời hạn đã chọn.',
  deleted: 'Đã xóa các mục đã chọn.', moved: 'Đã đổi tên hoặc di chuyển.', folderCreated: 'Đã tạo thư mục.',
  queuedTransfer: 'Đã thêm tác vụ vào hàng đợi.', selectOne: 'Vui lòng chọn đúng một đối tượng.',
  selectItems: 'Vui lòng chọn ít nhất một mục.', selectFile: 'Vui lòng chọn một tệp, không chọn thư mục.',
  selectBucket: 'Vui lòng mở một tab và chọn bucket.', selectProfile: 'Vui lòng chọn hồ sơ ở thanh bên trái.',
  noTarget: 'Hãy mở thêm một tab và chọn bucket đích trước khi sao chép.',
  invalidFolder: 'Nhập tên thư mục tương đối hợp lệ, không dùng dấu chấm hoặc dấu gạch chéo ngược.',
  invalidDrop: 'Không đọc được đường dẫn của tệp thả vào. Hãy dùng nút chọn tệp hoặc thư mục.',
  count: (selected, loaded) => `Đã chọn ${selected} trong ${loaded} mục đã tải.`,
  tabLabel: (name, number) => `${name} (${number})`, closeTab: name => `Đóng tab ${name}`,
  breadcrumb: 'Đường dẫn thư mục', breadcrumbSep: '/', up: 'Lên một cấp', root: 'Gốc bucket',
  sizeQueued: 'Đang chờ tính...', sizeRunning: 'Đang tính...',
  sizeRunningValue: size => `Đang tính... ≥ ${size}`,
  sizeDone: (size, count) => `${size} (${count} đối tượng)`,
  sizeError: 'Không tính được',
  bucketSize: value => `Dung lượng bucket: ${value}`,
  folderSize: value => ` | Thư mục hiện tại: ${value}`,
  bucketPolicy: 'Policy bucket',
  bucketArn: 'ARN bucket',
  policyTitle: name => `Policy của bucket ${name}`,
  policyWarning: 'Policy quyết định ai được truy cập bucket. Sai policy có thể chặn hoặc mở quyền truy cập ngoài ý muốn.',
  policyEmptyHint: 'Bucket chưa có policy. Đây chỉ là khung, hãy tự điền Statement và Resource theo ARN của nhà cung cấp (xem nút ARN bucket).',
  policyLabel: 'Nội dung policy (JSON)',
  savePolicy: 'Lưu policy',
  deletePolicy: 'Xóa policy',
  confirmDeletePolicy: name => `Xóa policy của bucket ${name}? Thao tác này không thể hoàn tác.`,
  invalidPolicyJson: 'Policy phải là một đối tượng JSON hợp lệ.',
  policySaved: 'Đã lưu policy của bucket.',
  policyDeleted: 'Đã xóa policy của bucket.',
  arnTitle: name => `ARN của bucket ${name}`,
  arnPresetLabel: 'Mẫu nhà cung cấp',
  arnPrefixLabel: 'Tiền tố ARN',
  arnPresetAws: 'arn:aws:s3::: (AWS S3, MinIO, Ceph RGW)',
  arnPresetEcs: 'urn:ecs:s3::: (Dell ECS, cần xác minh với tài liệu của nhà cung cấp)',
  arnBucket: 'ARN bucket',
  arnObjects: 'ARN mọi đối tượng',
  arnFolder: 'ARN thư mục hiện tại',
  copyText: 'Sao chép',
  arnHint: 'Định dạng ARN khác nhau theo nhà cung cấp S3. Nếu policy bị từ chối, hãy kiểm tra lại tiền tố với tài liệu của nhà cung cấp.',
  propertiesLabels: { size: 'Kích thước', etag: 'ETag', contentType: 'Loại nội dung', metadata: 'Metadata', lastModified: 'Ngày sửa' },
  unknown: 'Chưa xác định', error: 'Không thực hiện được thao tác.', shortcuts: 'Phím tắt: Delete xóa; F2 đổi tên; Ctrl+A chọn tất cả; Enter mở thư mục; Backspace lên một cấp.',
  source: 'Nguồn', destination: 'Đích', done: 'Xong',
};

      
const $ = id => document.getElementById(id);
const api = window.api;
const state = { profiles: [], tabs: [], active: null, selectedProfile: null, tasks: new Map(), nextTab: 1 };
const current = () => state.tabs.find(tab => tab.id === state.active);
const profileFor = id => state.profiles.find(profile => profile.id === id);
const selectedProfile = () => profileFor(state.selectedProfile);

for (const element of document.querySelectorAll('[data-i18n]')) element.textContent = t[element.dataset.i18n];
for (const element of document.querySelectorAll('[data-i18n-placeholder]')) element.placeholder = t[element.dataset.i18nPlaceholder];
for (const element of document.querySelectorAll('[data-i18n-title]')) element.title = t[element.dataset.i18nTitle];
for (const element of document.querySelectorAll('[data-i18n-aria]')) element.setAttribute('aria-label', t[element.dataset.i18nAria]);

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}
function button(text, action, className) {
  const node = element('button', text, className);
  node.type = 'button';
  node.addEventListener('click', () => run(action));
  return node;
}
function notify(message, error = false) { $('status').textContent = message; $('status').className = error ? 'error' : ''; }
async function run(action) {
  try { return await action(); }
  catch (error) { notify(error.message || t.error, true); }
}
const bytes = value => {
  if (value == null) return t.unknown;
  if (value < 1024) return `${value} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let unit = -1;
  do { value /= 1024; unit++; } while (value >= 1024 && unit < units.length - 1);
  return `${value.toLocaleString('vi-VN', { maximumFractionDigits: 1 })} ${units[unit]}`;
};
const date = value => value ? new Date(value).toLocaleString('vi-VN') : '';
const countText = value => (value ?? 0).toLocaleString('vi-VN');

let sizeTimer = null;
let lastSizeRender = 0;

function applyFolderSizeCell(cell, tab, key) {
  const info = tab?.sizes?.get(key);
  cell.removeAttribute('title');
  cell.className = '';
  if (!info || info.state === 'cancelled') {
    cell.textContent = t.folderType;
  } else if (info.state === 'queued') {
    cell.textContent = t.sizeQueued;
    cell.className = 'size-pending';
  } else if (info.state === 'running') {
    cell.textContent = info.bytes > 0 ? t.sizeRunningValue(bytes(info.bytes)) : t.sizeRunning;
    cell.className = 'size-pending';
  } else if (info.state === 'done') {
    cell.textContent = t.sizeDone(bytes(info.bytes ?? 0), countText(info.objects));
  } else if (info.state === 'error') {
    cell.textContent = t.sizeError;
    cell.className = 'size-error';
    if (info.message) cell.title = info.message;
  } else {
    cell.textContent = t.folderType;
  }
}

function formatSummarySize(info) {
  if (!info || info.state === 'queued' || info.state === 'running' || info.state === 'cancelled') return t.sizeRunning;
  if (info.state === 'done') return t.sizeDone(bytes(info.bytes ?? 0), countText(info.objects));
  if (info.state === 'error') return t.sizeError;
  return t.sizeRunning;
}

function renderSizeInfo() {
  const node = $('sizeInfo');
  const tab = current();
  if (!tab?.bucket) {
    node.hidden = true;
    node.textContent = '';
    node.removeAttribute('title');
    return;
  }
  node.hidden = false;
  const bucketInfo = tab.sizes.get('');
  let text = t.bucketSize(formatSummarySize(bucketInfo));
  if (tab.prefix) {
    const prefixInfo = tab.sizes.get(tab.prefix);
    text += t.folderSize(formatSummarySize(prefixInfo));
  }
  node.textContent = text;
  const errMsg = [bucketInfo, tab.prefix ? tab.sizes.get(tab.prefix) : null]
    .filter(item => item?.state === 'error' && item.message)
    .map(item => item.message)
    .join(' | ');
  if (errMsg) node.title = errMsg;
  else node.removeAttribute('title');
}

function applySizeUpdates() {
  const tab = current();
  renderSizeInfo();
  if (!tab?.bucket) return;
  const list = $('objects');
  const rowNodes = new Map();
  for (const tr of list.children) {
    const key = tr.dataset.key;
    if (!key) continue;
    rowNodes.set(key, tr);
    if (tr.dataset.folder === 'true' && tr.children[2]) {
      applyFolderSizeCell(tr.children[2], tab, key);
    }
  }
  if (tab.sort === 'size' && rowNodes.size > 1) {
    const sorted = rows(tab);
    let changed = false;
    for (let i = 0; i < sorted.length; i++) {
      if (list.children[i]?.dataset.key !== sorted[i].key) { changed = true; break; }
    }
    if (changed) {
      for (const row of sorted) {
        const tr = rowNodes.get(row.key);
        if (tr) list.append(tr);
      }
    }
  }
}

function scheduleSizeUpdate() {
  if (sizeTimer) return;
  const delay = Math.max(0, 250 - (Date.now() - lastSizeRender));
  sizeTimer = setTimeout(() => {
    sizeTimer = null;
    lastSizeRender = Date.now();
    applySizeUpdates();
  }, delay);
}

function requestSize(tab, prefix, force = false) {
  if (!tab?.bucket || typeof api.s3.sizeStart !== 'function') return;
  const existing = tab.sizes.get(prefix);
  if (!force && existing && (existing.state === 'done' || existing.state === 'queued' || existing.state === 'running')) return;
  tab.sizes.set(prefix, { state: 'queued', bytes: 0, objects: 0, message: '' });
  api.s3.sizeStart(tab.profileId, tab.bucket, prefix, force).then(started => {
    if (started === false && tab.sizes.get(prefix)?.state === 'queued') {
      tab.sizes.delete(prefix);
      if (tab.id === state.active) scheduleSizeUpdate();
    }
  }).catch(error => {
    if (tab.bucket) {
      tab.sizes.set(prefix, { state: 'error', bytes: 0, objects: 0, message: error?.message || t.error });
      if (tab.id === state.active) scheduleSizeUpdate();
    }
  });
}

function cancelAllSizes(tab) {
  if (!tab?.bucket || !tab.sizes) return;
  if (typeof api.s3.sizeCancel === 'function') {
    for (const [prefix, info] of tab.sizes.entries()) {
      if (info.state === 'queued' || info.state === 'running') {
        api.s3.sizeCancel(tab.profileId, tab.bucket, prefix).catch(() => {});
      }
    }
  }
  tab.sizes.clear();
}

function cancelOldFolderSizes(tab, nextPrefix) {
  if (!tab?.bucket || !tab.sizes) return;
  for (const [prefixKey, info] of tab.sizes.entries()) {
    if (!prefixKey || prefixKey === nextPrefix) continue;
    if (info.state === 'queued' || info.state === 'running') {
      tab.sizes.delete(prefixKey);
      if (typeof api.s3.sizeCancel === 'function') {
        api.s3.sizeCancel(tab.profileId, tab.bucket, prefixKey).catch(() => {});
      }
    }
  }
}

function ask(title, fields = [], message = '', submit = t.done) {
  if ($('actionDialog').open) return Promise.resolve(null);
  $('actionTitle').textContent = title;
  $('actionMessage').textContent = message;
  $('actionMessage').hidden = !message;
  $('actionSubmit').textContent = submit;
  $('actionFields').replaceChildren();
  for (const field of fields) {
    const label = element('label');
    label.append(element('span', field.label));
    const input = element(field.options ? 'select' : field.multiline ? 'textarea' : 'input');
    input.name = field.name;
    if (field.options) for (const option of field.options) {
      const node = element('option', option.label); node.value = option.value; input.append(node);
    }
    if (field.type) input.type = field.type;
    if (field.min !== undefined) input.min = field.min;
    if (field.max !== undefined) input.max = field.max;
    input.required = field.required ?? false;
    input.maxLength = field.maxLength || (field.multiline ? 2 * 1024 * 1024 : 1024);
    input.value = field.value || '';
    input.readOnly = field.readOnly || false;
    label.append(input); $('actionFields').append(label);
  }
  const dialog = $('actionDialog');
  return new Promise(resolve => {
    let value = null;
    $('actionForm').onsubmit = event => { event.preventDefault(); value = Object.fromEntries(new FormData(event.currentTarget)); dialog.close(); };
    $('actionCancel').onclick = () => dialog.close();
    dialog.addEventListener('close', () => { $('actionFields').replaceChildren(); resolve(value); }, { once: true });
    dialog.showModal();
  });
}

async function reloadProfiles() {
  state.profiles = await api.profiles.list();
  if (!profileFor(state.selectedProfile)) state.selectedProfile = state.profiles[0]?.id || null;
  render();
}

function renderProfiles() {
  const list = $('profiles'); list.replaceChildren();
  if (!state.profiles.length) list.append(element('li', t.noProfiles, 'empty'));
  for (const profile of state.profiles) {
    const item = element('li');
    const node = button(profile.name, () => { state.selectedProfile = profile.id; renderProfiles(); }, profile.id === state.selectedProfile ? 'active' : '');
    node.addEventListener('dblclick', () => run(() => openTab(profile)));
    item.append(node); list.append(item);
  }
  for (const name of ['editProfile', 'duplicateProfile', 'removeProfile', 'testProfile', 'openProfile']) $(name).disabled = !selectedProfile();
}

function render() {
  renderProfiles();
  const tabs = $('tabs'); tabs.replaceChildren();
  for (const tab of state.tabs) {
    const label = t.tabLabel(profileFor(tab.profileId)?.name || t.profiles, tab.number);
    const node = element('div', undefined, `tab${tab.id === state.active ? ' active' : ''}`);
    const open = button(label, () => { state.active = tab.id; state.selectedProfile = tab.profileId; render(); });
    open.setAttribute('role', 'tab'); open.setAttribute('aria-selected', tab.id === state.active ? 'true' : 'false');
    const close = button(t.close, () => {
      cancelAllSizes(tab);
      state.tabs = state.tabs.filter(item => item !== tab);
      if (state.active === tab.id) state.active = state.tabs.at(-1)?.id || null;
      render();
    });
    close.setAttribute('aria-label', t.closeTab(label)); node.append(open, close); tabs.append(node);
  }
  const tab = current();
  const buckets = $('buckets'); buckets.replaceChildren();
  if (!tab?.buckets.length) buckets.append(element('li', t.noBuckets, 'empty'));
  for (const bucket of tab?.buckets || []) {
    const item = element('li');
    item.append(button(bucket.name, () => navigate(tab, bucket.name, ''), bucket.name === tab.bucket ? 'active' : ''));
    buckets.append(item);
  }
  $('tlsWarning').hidden = !profileFor(tab?.profileId)?.allowInsecureTls;
  const breadcrumb = $('breadcrumb'); breadcrumb.replaceChildren();
  if (tab?.bucket) {
    breadcrumb.append(button(t.up, () => navigate(tab, tab.bucket, parent(tab.prefix))));
    breadcrumb.append(button(tab.bucket, () => navigate(tab, tab.bucket, '')));
    let accumulated = '';
    for (const segment of tab.prefix.split('/').filter(Boolean)) {
      accumulated += `${segment}/`;
      const destination = accumulated;
      const sep = element('span', t.breadcrumbSep, 'breadcrumb-sep');
      sep.setAttribute('aria-hidden', 'true');
      breadcrumb.append(sep, button(segment, () => navigate(tab, tab.bucket, destination)));
    }
  } else breadcrumb.textContent = tab ? t.noBucket : t.noTab;
  renderSizeInfo();
  $('filter').value = tab?.filter || '';
  $('sort').value = tab?.sort || 'name';
  $('sortDirection').textContent = tab?.descending ? t.descending : t.ascending;
  $('more').hidden = !tab?.nextToken;
  $('more').disabled = Boolean(tab?.loading);
  for (const name of ['bucketPolicy', 'bucketArn', 'upload', 'uploadFolder', 'download', 'createFolder', 'deleteObjects', 'move', 'copy', 'presign', 'properties', 'filter', 'sort', 'sortDirection', 'selectAll']) $(name).disabled = !tab?.bucket || Boolean(tab?.loading);
  $('refresh').disabled = !tab || Boolean(tab.loading);
  $('createBucket').disabled = !tab || Boolean(tab.loading);
  renderObjects();
}

function rowSize(tab, row) {
  if (!row.folder) return { known: true, bytes: row.size || 0 };
  const info = tab.sizes?.get(row.key);
  if (info?.state === 'done') return { known: true, bytes: info.bytes || 0 };
  return { known: false, bytes: 0 };
}

function rows(tab) {
  const query = tab.filter.toLocaleLowerCase('vi-VN');
  return tab.rows.filter(row => row.name.toLocaleLowerCase('vi-VN').includes(query)).sort((a, b) => {
    let value;
    if (tab.sort === 'size') {
      const sa = rowSize(tab, a);
      const sb = rowSize(tab, b);
      if (sa.known !== sb.known) return sa.known ? -1 : 1;
      value = sa.bytes - sb.bytes;
    } else if (tab.sort === 'modified') value = (new Date(a.lastModified || 0).getTime()) - (new Date(b.lastModified || 0).getTime());
    else value = a.name.localeCompare(b.name, 'vi');
    return (value || a.key.localeCompare(b.key, 'vi')) * (tab.descending ? -1 : 1);
  });
}

function renderObjects() {
  const tab = current();
  const list = $('objects'); list.replaceChildren();
  const visible = tab ? rows(tab) : [];
  $('empty').hidden = visible.length > 0;
  $('empty').textContent = tab?.loading ? t.loading : !tab ? t.noTab : !tab.bucket ? t.noBucket : t.noObjects;
  for (const row of visible) {
    const node = element('tr', undefined, tab.selected.has(row.key) ? 'selected' : '');
    node.dataset.key = row.key;
    node.dataset.folder = row.folder ? 'true' : 'false';
    const select = element('input'); select.type = 'checkbox'; select.checked = tab.selected.has(row.key); select.setAttribute('aria-label', row.name);
    const toggle = () => {
      if (tab.selected.has(row.key)) tab.selected.delete(row.key); else tab.selected.add(row.key);
      renderObjects();
    };
    select.addEventListener('change', toggle);
    const check = element('td'); check.append(select); node.append(check);
    const name = element('td');
    if (row.folder) name.append(button(row.name, () => navigate(tab, tab.bucket, row.key), 'object-link'));
    else name.textContent = row.name;
    const sizeCell = element('td');
    if (row.folder) applyFolderSizeCell(sizeCell, tab, row.key);
    else sizeCell.textContent = bytes(row.size);
    node.append(name, sizeCell, element('td', date(row.lastModified)));
    node.addEventListener('click', event => { if (!event.target.closest('button, input')) toggle(); });
    node.addEventListener('dblclick', event => { if (!event.target.closest('button, input')) run(() => row.folder ? navigate(tab, tab.bucket, row.key) : properties(tab, row.key)); });
    list.append(node);
  }
  const checked = visible.filter(row => tab.selected.has(row.key)).length;
  $('selectAll').checked = visible.length > 0 && checked === visible.length;
  $('selectAll').indeterminate = checked > 0 && checked < visible.length;
  $('selectionCount').textContent = t.count(tab?.selected.size || 0, tab?.rows.length || 0);
}

async function openTab(profile) {
  const tab = { id: crypto.randomUUID(), number: state.nextTab++, profileId: profile.id, buckets: [], bucket: '', prefix: '', rows: [], selected: new Set(), sizes: new Map(), nextToken: null, filter: '', sort: 'name', descending: false, request: 0, loading: false };
  state.tabs.push(tab); state.active = tab.id; state.selectedProfile = profile.id; render();
  await loadBuckets(tab, profile.defaultBucket);
}
async function loadBuckets(tab, preferred) {
  try { tab.buckets = await api.s3.listBuckets(tab.profileId); }
  catch (error) { notify(error.message, true); }
  if (preferred && !tab.buckets.some(bucket => bucket.name === preferred)) tab.buckets.push({ name: preferred });
  render();
  if (preferred) await navigate(tab, preferred, '');
}
async function navigate(tab, bucket, prefix, more = false, forceSize = false) {
  const request = ++tab.request;
  const token = more ? tab.nextToken : null;
  if (!more) {
    if (tab.bucket && tab.bucket !== bucket) cancelAllSizes(tab);
    else if (tab.bucket === bucket && tab.prefix !== prefix) cancelOldFolderSizes(tab, prefix);
  }
  tab.bucket = bucket; tab.prefix = prefix; tab.loading = true;
  const existingKeys = more ? new Set(tab.rows.map(row => row.key)) : null;
  if (!more) { tab.rows = []; tab.selected.clear(); tab.nextToken = null; }
  if (forceSize && tab.sizes) {
    tab.sizes.delete('');
    if (prefix) tab.sizes.delete(prefix);
  }
  render();
  try {
    const result = await api.s3.list(tab.profileId, bucket, prefix, token);
    if (tab.request !== request) return;
    const additions = [
      ...result.folders.map(folder => ({ key: folder.prefix, name: folder.prefix.slice(prefix.length), folder: true })),
      ...result.objects.map(object => ({ ...object, name: object.key.slice(prefix.length), folder: object.key.endsWith('/') })),
    ];
    tab.rows = [...new Map([...tab.rows, ...additions].map(row => [row.key, row])).values()];
    tab.nextToken = result.nextToken;
    if (more) {
      const addedFolders = new Set(additions.filter(row => row.folder && !existingKeys.has(row.key)).map(row => row.key));
      for (const row of rows(tab)) {
        if (row.folder && addedFolders.has(row.key)) requestSize(tab, row.key, false);
      }
    } else {
      const visibleFolders = rows(tab).filter(row => row.folder);
      if (forceSize) {
        for (const row of visibleFolders) tab.sizes.delete(row.key);
      }
      requestSize(tab, '', forceSize);
      if (prefix) requestSize(tab, prefix, forceSize);
      for (const row of visibleFolders) requestSize(tab, row.key, forceSize);
    }
  } finally { if (tab.request === request) tab.loading = false; render(); }
}
const parent = prefix => prefix.replace(/[^/]+\/$/, '');
function requireTab() { const tab = current(); if (!tab?.bucket) throw new Error(t.selectBucket); return tab; }
function selection(tab, one = false, file = false) {
  const keys = [...tab.selected];
  if (!keys.length) throw new Error(t.selectItems);
  if (one && keys.length !== 1) throw new Error(t.selectOne);
  if (file && keys[0].endsWith('/')) throw new Error(t.selectFile);
  return keys;
}
function requireProfile() { const profile = selectedProfile(); if (!profile) throw new Error(t.selectProfile); return profile; }

let editingId;
function editProfile(profile) {
  editingId = profile?.id;
  $('importProfiles').hidden = Boolean(profile);
  const form = $('profileForm'); form.reset();
  for (const name of ['name', 'endpoint', 'region', 'accessKeyId', 'defaultBucket']) form.elements[name].value = profile?.[name] || (name === 'region' ? 'us-east-1' : '');
  form.elements.forcePathStyle.checked = profile?.forcePathStyle ?? false;
  form.elements.allowInsecureTls.checked = profile?.allowInsecureTls ?? false;
  $('secretHint').textContent = profile?.hasSecret ? t.secretKeep : t.secretNew;
  $('profileError').textContent = '';
  $('profileDialog').showModal();
}
function draft() {
  const form = $('profileForm');
  return { id: editingId, name: form.elements.name.value, endpoint: form.elements.endpoint.value,
    region: form.elements.region.value, accessKeyId: form.elements.accessKeyId.value,
    secretAccessKey: form.elements.secretAccessKey.value, defaultBucket: form.elements.defaultBucket.value,
    forcePathStyle: form.elements.forcePathStyle.checked, allowInsecureTls: form.elements.allowInsecureTls.checked };
}
$('profileForm').elements.endpoint.addEventListener('input', event => { if (!editingId) $('profileForm').elements.forcePathStyle.checked = Boolean(event.target.value); });
$('profileForm').addEventListener('submit', async event => {
  event.preventDefault();
  const submit = event.currentTarget.querySelector('[type=submit]'); submit.disabled = true;
  try {
    const result = await api.profiles.save(draft()); state.selectedProfile = result.id;
    const affected = state.tabs.filter(tab => tab.profileId === result.id);
    for (const tab of affected) {
      cancelAllSizes(tab);
      tab.request++; tab.buckets = []; tab.bucket = ''; tab.prefix = ''; tab.rows = [];
      tab.selected.clear(); tab.nextToken = null; tab.loading = false;
    }
    $('profileDialog').close(); await reloadProfiles(); notify(t.saved);
    for (const tab of affected) await run(() => loadBuckets(tab, result.defaultBucket));
  } catch (error) { $('profileError').textContent = error.message; }
  finally { submit.disabled = false; }
});
$('profileDialog').addEventListener('close', () => { $('profileForm').elements.secretAccessKey.value = ''; });
$('cancelProfile').onclick = () => $('profileDialog').close();
$('testDraft').onclick = async () => {
  $('testDraft').disabled = true;
  try { await api.profiles.test(draft()); $('profileError').textContent = t.connectionOk; }
  catch (error) { $('profileError').textContent = error.message; }
  finally { $('testDraft').disabled = false; }
};

let policyRequest = 0;
let currentPolicyHasExisting = false;

async function openPolicyDialog() {
  const tab = requireTab();
  const req = ++policyRequest;
  const dialog = $('policyDialog');
  currentPolicyHasExisting = false;
  $('policyTitle').textContent = t.policyTitle(tab.bucket);
  $('policyError').textContent = '';
  $('policyEmptyHint').hidden = true;
  $('policyInput').value = '';
  $('policyInput').disabled = true;
  $('savePolicy').disabled = true;
  $('deletePolicy').disabled = true;
  if (!dialog.open) dialog.showModal();
  try {
    const policy = await api.s3.getBucketPolicy(tab.profileId, tab.bucket);
    if (req !== policyRequest || !dialog.open) return;
    currentPolicyHasExisting = Boolean(policy && policy.trim());
    $('policyEmptyHint').hidden = currentPolicyHasExisting;
    $('policyInput').value = currentPolicyHasExisting
      ? policy
      : JSON.stringify({ Version: '2012-10-17', Statement: [] }, null, 2);
    $('policyInput').disabled = false;
    $('savePolicy').disabled = false;
    $('deletePolicy').disabled = !currentPolicyHasExisting;
  } catch (error) {
    if (req !== policyRequest || !dialog.open) return;
    $('policyInput').disabled = false;
    $('savePolicy').disabled = false;
    $('deletePolicy').disabled = true;
    $('policyError').textContent = error.message || t.error;
  }
}

$('policyForm').addEventListener('submit', async event => {
  event.preventDefault();
  const tab = current();
  if (!tab?.bucket) return;
  const raw = $('policyInput').value.trim();
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (_err) {
    $('policyError').textContent = t.invalidPolicyJson;
    return;
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    $('policyError').textContent = t.invalidPolicyJson;
    return;
  }
  $('policyError').textContent = '';
  $('savePolicy').disabled = true;
  $('deletePolicy').disabled = true;
  try {
    await api.s3.putBucketPolicy(tab.profileId, tab.bucket, raw);
    $('policyDialog').close();
    notify(t.policySaved);
  } catch (error) {
    $('policyError').textContent = error.message || t.error;
    $('savePolicy').disabled = false;
    $('deletePolicy').disabled = !currentPolicyHasExisting;
  }
});
$('deletePolicy').onclick = async () => {
  const tab = current();
  if (!tab?.bucket) return;
  if (!await ask(t.deletePolicy, [], t.confirmDeletePolicy(tab.bucket), t.deletePolicy)) return;
  $('policyError').textContent = '';
  $('savePolicy').disabled = true;
  $('deletePolicy').disabled = true;
  try {
    await api.s3.deleteBucketPolicy(tab.profileId, tab.bucket);
    $('policyDialog').close();
    notify(t.policyDeleted);
  } catch (error) {
    $('policyError').textContent = error.message || t.error;
    $('savePolicy').disabled = false;
    $('deletePolicy').disabled = !currentPolicyHasExisting;
  }
};
$('cancelPolicy').onclick = () => $('policyDialog').close();

const ARN_PRESETS = [
  { value: 'arn:aws:s3:::', label: t.arnPresetAws },
  { value: 'urn:ecs:s3:::', label: t.arnPresetEcs },
];
for (const preset of ARN_PRESETS) {
  const opt = element('option', preset.label);
  opt.value = preset.value;
  $('arnPreset').append(opt);
}

function getSavedArnPrefix(profileId) {
  try {
    const saved = window.localStorage.getItem(`arnPrefix:${profileId}`);
    if (typeof saved === 'string' && saved.length > 0) return saved;
  } catch (_ignore) {}
  return ARN_PRESETS[0].value;
}

function saveArnPrefix(profileId, prefix) {
  if (!profileId) return;
  try {
    window.localStorage.setItem(`arnPrefix:${profileId}`, prefix);
  } catch (_ignore) {}
}

function renderArnRows() {
  const tab = current();
  if (!tab?.bucket) return;
  const prefix = $('arnPrefix').value;
  const items = [
    { label: t.arnBucket, value: `${prefix}${tab.bucket}` },
    { label: t.arnObjects, value: `${prefix}${tab.bucket}/*` },
  ];
  if (tab.prefix) {
    items.push({ label: t.arnFolder, value: `${prefix}${tab.bucket}/${tab.prefix}*` });
  }
  const list = $('arnList');
  list.replaceChildren();
  for (const item of items) {
    const row = element('div', undefined, 'arn-row');
    const label = element('span', item.label, 'arn-label');
    const code = element('code', item.value, 'arn-value');
    const copyBtn = button(t.copyText, async () => {
      $('arnError').textContent = '';
      try {
        await api.clipboard.writeText(item.value);
        notify(t.copied);
      } catch (error) {
        $('arnError').textContent = error.message || t.error;
      }
    });
    row.append(label, code, copyBtn);
    list.append(row);
  }
}

function openArnDialog() {
  const tab = requireTab();
  $('arnTitle').textContent = t.arnTitle(tab.bucket);
  $('arnError').textContent = '';
  const prefix = getSavedArnPrefix(tab.profileId);
  $('arnPrefix').value = prefix;
  if (ARN_PRESETS.some(item => item.value === prefix)) $('arnPreset').value = prefix;
  renderArnRows();
  $('arnDialog').showModal();
}

$('arnForm').addEventListener('submit', event => event.preventDefault());
$('arnPreset').addEventListener('change', event => {
  const tab = current();
  $('arnPrefix').value = event.target.value;
  if (tab) saveArnPrefix(tab.profileId, event.target.value);
  renderArnRows();
});
$('arnPrefix').addEventListener('input', event => {
  const tab = current();
  const value = event.target.value;
  if (ARN_PRESETS.some(item => item.value === value)) $('arnPreset').value = value;
  if (tab) saveArnPrefix(tab.profileId, value);
  renderArnRows();
});
$('closeArn').onclick = () => $('arnDialog').close();

async function upload(directory) {
  const tab = requireTab();
  const paths = await api.dialogs.upload(directory);
  if (paths.length) { await api.transfers.upload(tab.profileId, tab.bucket, tab.prefix, paths); notify(t.queuedTransfer); }
}
async function download() {
  const tab = requireTab(); const keys = selection(tab);
  const directory = await api.dialogs.downloadDirectory();
  if (directory) { await api.transfers.download(tab.profileId, tab.bucket, keys, directory); notify(t.queuedTransfer); }
}
async function deleteObjects() {
  const tab = requireTab(); const keys = selection(tab);
  if (!await ask(t.remove, [], t.confirmDelete(keys.length), t.remove)) return;
  const folders = keys.filter(key => key.endsWith('/')).filter((key, _, all) => !all.some(other => other !== key && key.startsWith(other)));
  const files = keys.filter(key => !key.endsWith('/') && !folders.some(folder => key.startsWith(folder)));
  try {
    for (const folder of folders) await api.s3.deletePrefix(tab.profileId, tab.bucket, folder);
    if (files.length) await api.s3.delete(tab.profileId, tab.bucket, files);
    notify(t.deleted);
  } finally {
    tab.sizes.clear();
    await navigate(tab, tab.bucket, tab.prefix);
  }
}
async function move() {
  const tab = requireTab(); const [key] = selection(tab, true);
  const result = await ask(t.move, [{ name: 'key', label: t.targetKey, value: key, required: true }], t.confirmMove);
  if (result) { await api.s3.move(tab.profileId, tab.bucket, key, result.key); tab.sizes.clear(); notify(t.moved); await navigate(tab, tab.bucket, tab.prefix); }
}
async function properties(tab, key) {
  const data = await api.s3.head(tab.profileId, tab.bucket, key);
  const text = Object.entries(t.propertiesLabels).map(([name, label]) => `${label}: ${name === 'lastModified' ? date(data[name]) : name === 'size' ? bytes(data[name]) : typeof data[name] === 'object' ? JSON.stringify(data[name], null, 2) : data[name] || ''}`).join('\n');
  await ask(t.properties, [{ name: 'properties', label: key, multiline: true, readOnly: true, value: text }]);
}
async function copy() {
  const source = requireTab(); const keys = selection(source);
  const targets = state.tabs.filter(tab => tab !== source && tab.bucket);
  if (!targets.length) throw new Error(t.noTarget);
  const result = await ask(t.copy, [
    { name: 'tab', label: t.targetTab, value: targets[0].id, options: targets.map(tab => ({ value: tab.id, label: `${t.tabLabel(profileFor(tab.profileId)?.name || '', tab.number)}: ${tab.bucket}/${tab.prefix}` })) },
    { name: 'prefix', label: t.targetPrefix, value: targets[0].prefix },
  ], t.confirmCopy);
  if (!result) return;
  const target = state.tabs.find(tab => tab.id === result.tab);
  if (!target?.bucket) throw new Error(t.noTarget);
  const prefix = result.prefix && !result.prefix.endsWith('/') ? `${result.prefix}/` : result.prefix;
  await api.transfers.copy(source.profileId, source.bucket, keys, target.profileId, target.bucket, prefix);
  notify(t.queuedTransfer);
}

const actions = {
  addProfile: () => editProfile(), editProfile: () => editProfile(requireProfile()),
  openProfile: () => openTab(requireProfile()),
  duplicateProfile: async () => { await api.profiles.duplicate(requireProfile().id); await reloadProfiles(); notify(t.duplicated); },
  removeProfile: async () => {
    const profile = requireProfile();
    if (await ask(t.remove, [], t.confirmRemoveProfile, t.remove)) {
      await api.profiles.remove(profile.id);
      for (const tab of state.tabs) if (tab.profileId === profile.id) cancelAllSizes(tab);
      state.tabs = state.tabs.filter(tab => tab.profileId !== profile.id);
      if (!current()) state.active = state.tabs.at(-1)?.id || null;
      await reloadProfiles(); notify(t.removed);
    }
  },
  testProfile: async () => { await api.profiles.test(requireProfile().id); notify(t.connectionOk); },
  importProfiles: async () => {
    const result = await ask(t.importTitle, [{ name: 'json', label: t.json, multiline: true, required: true }]);
    if (!result) return;
    try {
      await api.profiles.import(result.json);
      if ($('profileDialog').open) $('profileDialog').close();
      await reloadProfiles(); notify(t.imported);
    } catch (error) {
      if ($('profileDialog').open) $('profileError').textContent = error.message || t.error;
      throw error;
    }
  },
  exportProfiles: async () => {
    const json = await api.profiles.export();
    if (await ask(t.exportTitle, [{ name: 'json', label: t.json, multiline: true, readOnly: true, value: json }], '', t.copyJson)) { await api.clipboard.writeText(json); notify(t.copied); }
  },
  bucketPolicy: () => openPolicyDialog(),
  bucketArn: () => openArnDialog(),
  refresh: async () => { const tab = current(); if (tab?.bucket) await navigate(tab, tab.bucket, tab.prefix, false, true); else if (tab) await loadBuckets(tab); },
  more: () => { const tab = requireTab(); return navigate(tab, tab.bucket, tab.prefix, true); },
  upload: () => upload(false), uploadFolder: () => upload(true), download, deleteObjects, move, copy,
  createBucket: async () => {
    const tab = current(); if (!tab) throw new Error(t.noTab);
    const result = await ask(t.createBucket, [{ name: 'name', label: t.bucketName, required: true, maxLength: 63 }]);
    if (!result) return;
    const { name } = await api.s3.createBucket(tab.profileId, result.name.trim());
    notify(t.bucketCreated); await loadBuckets(tab, name);
  },
  createFolder: async () => {
    const tab = requireTab();
    const result = await ask(t.createFolder, [{ name: 'name', label: t.folderName, required: true }]);
    if (!result) return;
    const name = result.name.replace(/\/$/, '');
    if (!name || name.includes('\\') || name.split('/').some(part => !part || part === '.' || part === '..')) throw new Error(t.invalidFolder);
    await api.s3.createFolder(tab.profileId, tab.bucket, `${tab.prefix}${name}/`); tab.sizes.clear(); notify(t.folderCreated); await navigate(tab, tab.bucket, tab.prefix);
  },
  properties: () => { const tab = requireTab(); return properties(tab, selection(tab, true, true)[0]); },
  presign: async () => {
    const tab = requireTab(); const [key] = selection(tab, true, true);
    const result = await ask(t.presign, [{ name: 'expiry', label: t.expiry, type: 'number', min: 1, max: 604800, value: '3600', required: true }], t.urlWarning);
    if (result) { const url = await api.s3.presign(tab.profileId, tab.bucket, key, Number(result.expiry)); await api.clipboard.writeText(url); notify(t.copied); }
  },
  sortDirection: () => { const tab = current(); if (tab) { tab.descending = !tab.descending; render(); } },
};
for (const [id, action] of Object.entries(actions)) $(id).addEventListener('click', () => run(action));
$('filter').oninput = event => {
  const tab = current();
  if (tab) {
    tab.filter = event.target.value;
    for (const row of rows(tab)) if (row.folder) requestSize(tab, row.key);
    renderObjects();
  }
};
$('sort').onchange = event => { const tab = current(); if (tab) { tab.sort = event.target.value; renderObjects(); } };
function toggleAll(checked) { const tab = current(); if (tab) { for (const row of rows(tab)) { if (checked) tab.selected.add(row.key); else tab.selected.delete(row.key); } renderObjects(); } }
$('selectAll').onchange = event => toggleAll(event.target.checked);
document.addEventListener('keydown', event => {
  if (document.querySelector('dialog[open]') || event.target.closest('input, select, textarea, button')) return;
  const tab = current(); if (!tab?.bucket) return;
  let action;
  if (event.key === 'Delete') action = deleteObjects;
  if (event.key === 'F2') action = move;
  if (event.ctrlKey && event.key.toLowerCase() === 'a') action = () => toggleAll(true);
  if (event.key === 'Backspace') action = () => navigate(tab, tab.bucket, parent(tab.prefix));
  if (event.key === 'Enter' && tab.selected.size === 1) {
    const [key] = tab.selected;
    if (key.endsWith('/')) action = () => navigate(tab, tab.bucket, key);
  }
  if (action) { event.preventDefault(); run(action); }
});
document.addEventListener('dragover', event => event.preventDefault());
document.addEventListener('drop', event => event.preventDefault());
$('dropZone').addEventListener('dragover', event => { event.preventDefault(); $('dropZone').classList.add('dragging'); });
$('dropZone').addEventListener('dragleave', () => $('dropZone').classList.remove('dragging'));
// Wails cấp đường dẫn tệp thả vào qua OnFileDrop; sự kiện drop của DOM không có đường dẫn.
api.files.onDrop(paths => {
  $('dropZone').classList.remove('dragging');
  run(async () => {
    const tab = requireTab();
    if (!paths?.length || paths.some(value => !value)) throw new Error(t.invalidDrop);
    await api.transfers.upload(tab.profileId, tab.bucket, tab.prefix, paths); notify(t.queuedTransfer);
  });
});

function renderTransfers() {
  const list = $('transfers'); list.replaceChildren();
  if (!state.tasks.size) { const row = element('tr'); const cell = element('td', t.noTransfers); cell.colSpan = 5; row.append(cell); list.append(row); }
  for (const job of [...state.tasks.values()].reverse()) {
    const row = element('tr');
    const name = element('td', `${t[`${job.type}Type`]}: ${job.name}`);
    const progress = element('td');
    const bar = element('progress');
    if (job.total != null) { bar.max = job.total || 1; bar.value = job.status === 'completed' ? bar.max : job.transferred; }
    bar.setAttribute('aria-label', t.progress);
    progress.append(bar, element('div', `${bytes(job.transferred)} / ${bytes(job.total)}`));
    const status = element('td', t[job.status]);
    if (job.error) status.append(element('div', job.error.message, 'task-error'));
    const actions = element('td');
    if (['queued', 'running'].includes(job.status)) actions.append(button(t.cancel, () => api.transfers.cancel(job.id)));
    if (['failed', 'cancelled'].includes(job.status)) actions.append(button(t.retry, () => api.transfers.retry(job.id)));
    row.append(name, progress, element('td', `${bytes(job.speed)}/s`), status, actions); list.append(row);
  }
}
$('concurrency').onchange = event => run(async () => { await api.transfers.setConcurrency(Number(event.target.value)); });
api.transfers.onUpdate(job => { state.tasks.set(job.id, job); renderTransfers(); });
if (typeof api.s3.onSize === 'function') {
  api.s3.onSize(update => {
    if (!update || !update.profileId || !update.bucket) return;
    const prefix = update.prefix || '';
    let activeUpdated = false;
    for (const tab of state.tabs) {
      if (tab.profileId === update.profileId && tab.bucket === update.bucket) {
        tab.sizes.set(prefix, {
          state: update.state,
          bytes: update.bytes ?? 0,
          objects: update.objects ?? 0,
          message: update.message || '',
        });
        if (tab.id === state.active) activeUpdated = true;
      }
    }
    if (activeUpdated) scheduleSizeUpdate();
  });
}
await run(async () => {
  await reloadProfiles();
  for (const job of await api.transfers.list()) state.tasks.set(job.id, job);
  renderTransfers();
  document.documentElement.dataset.ready = 'true';
});

    }, 50);
  });
})();
