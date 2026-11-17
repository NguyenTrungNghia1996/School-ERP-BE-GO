INSERT INTO reserved_subdomains (keyword, reason) VALUES
('admin', 'Cổng quản trị nền tảng'),
('api', 'Cổng API tập trung'),
('tenant-ingress', 'Ingress định tuyến subdomain cho các Tenant'),
('cdn', 'Mạng phân phối nội dung'),
('image', 'Dịch vụ lưu trữ và phân phối hình ảnh')
ON CONFLICT (keyword) DO NOTHING;
