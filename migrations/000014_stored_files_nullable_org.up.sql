-- Cho phép lưu trữ tệp media cấp Platform (không gắn với organization_id cụ thể, VD: avatar admin nền tảng)
ALTER TABLE stored_files
    ALTER COLUMN organization_id DROP NOT NULL;

CREATE INDEX IF NOT EXISTS idx_stored_files_platform_created
    ON stored_files (created_at DESC)
    WHERE organization_id IS NULL AND deleted_at IS NULL;
