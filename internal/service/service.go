package service

import (
	"s3browser/internal/backend"
	"s3browser/internal/profiles"
	"s3browser/internal/s3svc"
	"s3browser/internal/transfers"
	"s3browser/internal/validate"
)

// Service chứa toàn bộ phương thức nghiệp vụ dùng chung giữa bản desktop và bản web.
type Service struct {
	Backend *backend.Backend
}

func New(b *backend.Backend) *Service {
	return &Service{Backend: b}
}

func Call[T any](s *Service, check error, ids []string, fn func() (T, error)) (T, error) {
	var zero T
	if s.Backend.Err != nil {
		return zero, s.Backend.Filter.Describe(s.Backend.Err)
	}
	if check != nil {
		return zero, s.Backend.Filter.Describe(check)
	}
	if len(ids) > 0 {
		done, err := s.Backend.Begin(ids...)
		if err != nil {
			return zero, s.Backend.Filter.Describe(err)
		}
		defer done()
	}
	result, err := fn()
	if err != nil {
		return zero, s.Backend.Filter.Describe(err)
	}
	return result, nil
}

func target(id, bucket string) error {
	return validate.First(validate.ID(id), validate.Bucket(bucket))
}

func (s *Service) ProfilesList() ([]profiles.Profile, error) {
	return Call(s, nil, nil, s.Backend.Profiles.List)
}

func (s *Service) ProfilesSave(p profiles.Profile) (profiles.Profile, error) {
	return Call(s, nil, nil, func() (profiles.Profile, error) { return s.Backend.Save(p) })
}

func (s *Service) ProfilesRemove(id string) (bool, error) {
	return Call(s, validate.ID(id), nil, func() (bool, error) { return s.Backend.Remove(id) })
}

func (s *Service) ProfilesDuplicate(id string) (profiles.Profile, error) {
	return Call(s, validate.ID(id), []string{id}, func() (profiles.Profile, error) { return s.Backend.Profiles.Duplicate(id) })
}

func (s *Service) ProfilesTestID(id string) ([]s3svc.Bucket, error) {
	return s.S3ListBuckets(id)
}

func (s *Service) ProfilesTestDraft(p profiles.Profile) ([]s3svc.Bucket, error) {
	s.Backend.Filter.Remember(p.AccessKeyID, p.SecretAccessKey)
	var ids []string
	if p.ID != "" {
		ids = []string{p.ID}
	}
	return Call(s, nil, ids, func() ([]s3svc.Bucket, error) { return s.Backend.S3.TestDraft(s.Backend.Context(), p) })
}

func (s *Service) ProfilesImport(input string) ([]profiles.Profile, error) {
	return Call(s, validate.Text(input, "JSON nhập", 2*1024*1024, false), nil, func() ([]profiles.Profile, error) { return s.Backend.Profiles.Import(input) })
}

func (s *Service) ProfilesExport() (string, error) {
	return Call(s, nil, nil, s.Backend.Profiles.Export)
}

func (s *Service) S3ListBuckets(id string) ([]s3svc.Bucket, error) {
	return Call(s, validate.ID(id), []string{id}, func() ([]s3svc.Bucket, error) { return s.Backend.S3.ListBuckets(s.Backend.Context(), id) })
}

func (s *Service) S3CreateBucket(id, name string) (s3svc.Bucket, error) {
	return Call(s, validate.First(validate.ID(id), validate.Text(name, "Tên bucket", 63, false)), []string{id}, func() (s3svc.Bucket, error) {
		return s.Backend.S3.CreateBucket(s.Backend.Context(), id, name)
	})
}

func (s *Service) S3GetBucketPolicy(id, bucket string) (string, error) {
	return Call(s, validate.First(target(id, bucket)), []string{id}, func() (string, error) {
		return s.Backend.S3.GetBucketPolicy(s.Backend.Context(), id, bucket)
	})
}

func (s *Service) S3PutBucketPolicy(id, bucket, policy string) (bool, error) {
	return Call(s, validate.First(target(id, bucket), validate.Text(policy, "Policy", 20480, false)), []string{id}, func() (bool, error) {
		err := s.Backend.S3.PutBucketPolicy(s.Backend.Context(), id, bucket, policy)
		return err == nil, err
	})
}

func (s *Service) S3DeleteBucketPolicy(id, bucket string) (bool, error) {
	return Call(s, validate.First(target(id, bucket)), []string{id}, func() (bool, error) {
		err := s.Backend.S3.DeleteBucketPolicy(s.Backend.Context(), id, bucket)
		return err == nil, err
	})
}

func (s *Service) S3List(id, bucket, prefix, token string) (s3svc.ListResult, error) {
	return Call(s, validate.First(target(id, bucket), validate.Prefix(prefix), validate.Text(token, "Token phân trang", 16384, true)), []string{id}, func() (s3svc.ListResult, error) {
		return s.Backend.S3.List(s.Backend.Context(), id, bucket, prefix, token)
	})
}

func (s *Service) S3Head(id, bucket, key string) (s3svc.HeadResult, error) {
	return Call(s, validate.First(target(id, bucket), validate.Key(key)), []string{id}, func() (s3svc.HeadResult, error) { return s.Backend.S3.Head(s.Backend.Context(), id, bucket, key) })
}

func (s *Service) S3Delete(id, bucket string, keys []string) (int, error) {
	return Call(s, validate.First(target(id, bucket), validate.Strings(keys, 10000, validate.Key)), []string{id}, func() (int, error) {
		return s.Backend.S3.Delete(s.Backend.Context(), id, bucket, keys)
	})
}

func (s *Service) S3DeletePrefix(id, bucket, prefix string) (int, error) {
	return Call(s, validate.First(target(id, bucket), validate.Key(prefix), validate.Prefix(prefix)), []string{id}, func() (int, error) {
		return s.Backend.S3.DeletePrefix(s.Backend.Context(), id, bucket, prefix)
	})
}

func (s *Service) S3CreateFolder(id, bucket, prefix string) (bool, error) {
	return Call(s, validate.First(target(id, bucket), validate.Prefix(prefix)), []string{id}, func() (bool, error) {
		return s.Backend.S3.CreateFolder(s.Backend.Context(), id, bucket, prefix)
	})
}

func (s *Service) S3Move(id, bucket, from, to string) (int, error) {
	return Call(s, validate.First(target(id, bucket), validate.Key(from), validate.Key(to)), []string{id}, func() (int, error) {
		return s.Backend.S3.Move(s.Backend.Context(), id, bucket, from, to)
	})
}

func (s *Service) S3Presign(id, bucket, key string, expirySec int) (string, error) {
	return Call(s, validate.First(target(id, bucket), validate.Key(key)), []string{id}, func() (string, error) {
		return s.Backend.S3.Presign(s.Backend.Context(), id, bucket, key, expirySec)
	})
}

func (s *Service) S3SizeStart(id, bucket, prefix string, force bool) (bool, error) {
	return Call(s, validate.First(target(id, bucket), validate.Prefix(prefix)), nil, func() (bool, error) { return s.Backend.Sizer.Start(id, bucket, prefix, force) })
}

func (s *Service) S3SizeCancel(id, bucket, prefix string) (bool, error) {
	return Call(s, validate.First(target(id, bucket), validate.Prefix(prefix)), nil, func() (bool, error) { return s.Backend.Sizer.Cancel(id, bucket, prefix) })
}

func (s *Service) TransfersUpload(id, bucket, prefix string, paths []string) ([]transfers.Job, error) {
	return Call(s, validate.First(target(id, bucket), validate.Prefix(prefix), validate.Strings(paths, 1000, func(v string) error {
		return validate.Text(v, "Đường dẫn tệp", 32767, false)
	})), []string{id}, func() ([]transfers.Job, error) {
		return s.Backend.Transfers.Upload(id, bucket, prefix, paths)
	})
}

func (s *Service) TransfersDownload(id, bucket string, keys []string, dir string) ([]transfers.Job, error) {
	return Call(s, validate.First(target(id, bucket), validate.Strings(keys, 10000, validate.Key), validate.Text(dir, "Thư mục đích", 32767, false)), []string{id}, func() ([]transfers.Job, error) {
		if err := s.Backend.CheckDirectory(dir); err != nil {
			return nil, err
		}
		return s.Backend.Transfers.Download(id, bucket, keys, dir)
	})
}

func (s *Service) TransfersCopy(srcID, srcBucket string, keys []string, dstID, dstBucket, prefix string) ([]transfers.Job, error) {
	return Call(s, validate.First(target(srcID, srcBucket), target(dstID, dstBucket), validate.Strings(keys, 10000, validate.Key), validate.Prefix(prefix)), []string{srcID, dstID}, func() ([]transfers.Job, error) {
		return s.Backend.Transfers.Copy(srcID, srcBucket, keys, dstID, dstBucket, prefix)
	})
}

func (s *Service) TransfersCancel(id string) (bool, error) {
	return Call(s, validate.ID(id), nil, func() (bool, error) { return s.Backend.Transfers.Cancel(id) })
}

func (s *Service) TransfersRetry(id string) (bool, error) {
	return Call(s, validate.ID(id), nil, func() (bool, error) { return s.Backend.Retry(id) })
}

func (s *Service) TransfersList() ([]transfers.Job, error) {
	return Call(s, nil, nil, func() ([]transfers.Job, error) { return s.Backend.Transfers.List(), nil })
}

func (s *Service) TransfersSetConcurrency(n int) (int, error) {
	return Call(s, nil, nil, func() (int, error) { return s.Backend.Transfers.SetConcurrency(n) })
}
