# SCHOOL ERP SAAS — BỘ KHUNG QUẢN LÝ TÀI LIỆU DỰ ÁN

**Phiên bản:** 1.1  
**Trạng thái:** Baseline cập nhật; các quyết định gói dịch vụ, RBAC và menu động đã được người dùng xác nhận ngày 2026-11-13; chi tiết triển khai tiếp tục đặc tả  
**Ngôn ngữ:** Tiếng Việt  
**Công nghệ định hướng:** Go, Fiber, GORM, PostgreSQL

## 1. Mục đích

Tài liệu này là mục lục chuẩn và nguồn tham chiếu ban đầu cho dự án School ERP SaaS. Mỗi gói được phát triển trong một cuộc chat riêng thuộc Project **dự án ứng dụng**. Khi thay đổi quyết định ảnh hưởng nhiều gói, phải cập nhật PK00 và ghi rõ các gói bị ảnh hưởng.

## 2. Baseline kiến trúc đã thống nhất

- Sản phẩm: hệ thống quản lý trường học đa tổ chức, đa trường, phục vụ nhiều cấp học.
- Mô hình triển khai: SaaS.
- Database: shared database, shared schema trên PostgreSQL.
- Backend: Go + Fiber + GORM.
- Frontend định hướng: React + TypeScript cho web; Flutter cho mobile.
- Kiến trúc khởi đầu: Modular Monolith, tách module theo bounded context; chưa chia microservices khi chưa có nhu cầu vận hành rõ ràng.
- API: REST, đặc tả OpenAPI.
- Hạ tầng định hướng: Docker; Redis cho cache/session/queue phù hợp; RabbitMQ cho xử lý bất đồng bộ khi cần; S3/MinIO cho tệp.
- Tài khoản: single account identity; quyền truy cập được xác định qua membership, role, permission và data scope.
- Phạm vi cấp học: hỗ trợ nhiều cấp học, cấu hình theo Organization/School.

## 3. Mô hình Organization – School – User

### 3.1 Tổ chức và trường

- Một Organization có đúng một subdomain chính.
- Một Organization có thể quản lý nhiều School.
- Subdomain định danh Organization, không định danh School.
- Một Organization được đăng ký và kiểm tra subdomain trước khi kích hoạt.
- Subdomain lowercase, không dấu; chỉ `a-z`, `0-9`, `-`; đề xuất dài 3–40 ký tự; không bắt đầu/kết thúc bằng dấu gạch ngang; có danh sách từ khóa reserved.
- Subdomain phải unique ở database. Lịch sử đổi subdomain và danh sách subdomain bị giữ chỗ cần được quản lý.
- Không cho đổi subdomain tùy tiện; thay đổi cần quy trình phê duyệt, audit và chính sách redirect.

### 3.2 Định tuyến tên miền tham khảo

- `school-erp.vn`: website/đăng ký.
- `app.school-erp.vn`: đăng nhập chung.
- `admin.school-erp.vn`: cổng Super Admin.
- `{subdomain}.school-erp.vn`: cổng Organization.
- `api.school-erp.vn`: API.

Cần hỗ trợ wildcard DNS/TLS. Tenant resolver xác định Organization từ Host đã được reverse proxy xác thực. Không tin `organization_id` do frontend tự gửi hoặc header tùy ý. Nếu dùng API domain chung, tenant context phải được xác minh với Host, token và membership.

### 3.3 Vai trò người dùng

Các nhóm chính:
1. Super Admin cấp nền tảng.
2. Admin trường.
3. Giáo viên.
4. Công chức/nhân viên trường (phân thành role nghiệp vụ cụ thể, không cấp một role STAFF quá rộng).
5. Học sinh/sinh viên.
6. Gia đình/phụ huynh/người giám hộ.

`users` là danh tính đăng nhập duy nhất. Không dùng `account_type` làm cơ chế phân quyền. Quyền thực tế đến từ membership, roles, permissions và data scope.

- `school_memberships`: liên kết user với school, có unique `(school_id, user_id)`.
- Một user có thể có nhiều role trong cùng một trường.
- `student_accounts`: liên kết user đăng nhập với hồ sơ học sinh.
- `guardians` và `student_guardians`: liên kết tài khoản người giám hộ với học sinh; quyền phụ huynh phải được kiểm soát theo từng quan hệ học sinh–người giám hộ.
- Một phụ huynh có thể có nhiều con. Không tự động mở quyền giữa các Organization khác nhau.
- Theo baseline Single, Admin trường/giáo viên/nhân viên mặc định gắn với một trường; nếu nghiệp vụ cần nhiều trường thì phải mở rộng membership có chủ đích.

### 3.4 Super Admin truy cập vào trường

Super Admin là tài khoản cấp nền tảng, có thể quản trị Organization/School và hỗ trợ trực tiếp tại từng trường thông qua **Support Mode / Delegated Support Session**.

Nguyên tắc:
- Không giả mạo tài khoản hoặc mật khẩu School Admin.
- Không sửa role trong DB để giả lập người dùng.
- Phiên hỗ trợ gắn actor, Organization, School, lý do, quyền được cấp, thời hạn và người phê duyệt nếu cần.
- Có thể giới hạn read-only hoặc cấp quyền thao tác được duyệt.
- Hiển thị banner Support Mode.
- Ghi audit đầy đủ; hết hạn hoặc revoke phải vô hiệu ngay.

Bảng tham khảo `support_access_sessions`: actor_user_id, organization_id, school_id, session_type, reason, permissions JSONB, approved_by, started_at, expires_at, revoked_at.

## 4. Nguyên tắc an toàn dữ liệu đa tenant

- Mọi dữ liệu tenant-scoped phải mang `organization_id`; dữ liệu thuộc trường phải mang thêm `school_id` khi phù hợp.
- Dùng composite foreign keys để ngăn liên kết chéo Organization/School, không chỉ dựa vào kiểm tra ở tầng ứng dụng.
- PostgreSQL Row Level Security (RLS) là lớp phòng vệ bổ sung.
- Bật `FORCE ROW LEVEL SECURITY` cho bảng phù hợp; DB application role không phải superuser và không có `BYPASSRLS`.
- Khi dùng connection pool, đặt tenant context trong transaction bằng `SET LOCAL`, không dùng `SET` toàn connection.
- GORM phải thực hiện `SET LOCAL app.organization_id` và `app.school_id` trong cùng transaction/connection trước các truy vấn tenant-scoped.
- Kiểm thử cách ly tại API, repository, DB/RLS và file storage.
- File/object storage phải có tenant-aware authorization; không xem URL khó đoán là cơ chế phân quyền.

## 5. Danh mục gói tài liệu

### PK00 — Tổng quan & quản trị dự án
Mục tiêu, đối tượng, phạm vi sản phẩm, glossary, nguyên tắc kiến trúc, quyết định đã chốt, decision log, roadmap, phụ thuộc giữa gói.

### PK01 — Kiến trúc tổng thể
C4 context/container/component, module boundaries, luồng request, tenant middleware, transaction strategy, logging, cache, event, background jobs, ADR.

### PK02 — Đa tổ chức & đa trường
Organization lifecycle, đăng ký/duyệt/kích hoạt, subdomain lifecycle, School lifecycle, tenant resolver, custom domain nếu có, tenant isolation, Support Mode.

### PK03 — Tài khoản & phân quyền
Authentication, SSO/MFA nếu cần, user lifecycle, membership, RBAC, permission catalog, data scope, guardian/student identity, session và token. Bổ sung: Organization tự tạo Role; Role chỉ được gán Permission thuộc Feature entitlement; User có thể có nhiều Role; phiên bản đầu không cấp quyền trực tiếp cho từng User; hỗ trợ Role hệ thống và Role tùy chỉnh theo phạm vi.

### PK04 — Cơ sở dữ liệu
ERD tổng thể, naming convention, UUID, schema theo module, constraints, composite FK, index, soft delete, migration, RLS policies, seed data. Bổ sung nhóm bảng gói dịch vụ/entitlement, Feature/Menu, RBAC, Organization menu customization; thiết kế phiên bản gói và subscription chính + Add-on; ràng buộc tenant, audit và hiệu lực.

### PK05 — Nghiệp vụ đào tạo
Cấp học, năm học, học kỳ, khối, lớp, môn học, chương trình, phân công giảng dạy, thời khóa biểu, lịch học và quy tắc chuyển lớp.

### PK06 — Học sinh & gia đình
Hồ sơ học sinh, nhập học, người giám hộ, nhiều con, quan hệ và quyền theo từng con, hồ sơ/tài liệu, chuyển trường, thôi học, bảo lưu.

### PK07 — Nhân sự & tổ chức trường
Cơ cấu tổ chức, phòng ban, chức vụ, hồ sơ giáo viên/nhân viên, hợp đồng, phân công, nghỉ phép, hồ sơ nghiệp vụ và phân quyền nhân sự.

### PK08 — Các phân hệ quản lý trường
Điểm danh, đánh giá/điểm, học bạ, tài chính/học phí, thu chi, thư viện, y tế, tuyển sinh, văn thư, tài sản, tư vấn và báo cáo nghiệp vụ. Mỗi phân hệ cần đặc tả độc lập bên trong gói.

### PK09 — API & tích hợp
API conventions, versioning, OpenAPI, error model, pagination/filter/sort, idempotency, webhook, notification, payment gateway, import/export và tích hợp bên thứ ba. Bổ sung API quản lý Package/Subscription/Add-on, Feature entitlement, Role/Permission, menu nền và menu tùy chỉnh; middleware/authorization policy kiểm tra subscription, feature, permission và data scope.

### PK10 — Giao diện & ứng dụng
Design system, responsive web, Platform Admin, School Admin, Teacher Portal, Staff Portal, Student Portal, Family Portal, mobile app, navigation và accessibility. Bổ sung màn hình quản lý Feature/Package/Subscription cho Super Admin; quản lý Role/Permission và menu cho Organization; menu động kết hợp menu nền với menu riêng của Organization, chỉ hiển thị theo entitlement và quyền của tài khoản.

### PK11 — Hạ tầng, bảo mật & vận hành
Môi trường dev/staging/production, Docker, CI/CD, secrets, TLS, WAF/rate limit, backup/restore, disaster recovery, observability, audit, retention, privacy và incident response.

### PK12 — Kiểm thử & triển khai
Test strategy, unit/integration/e2e, RLS/tenant isolation tests, performance/load tests, UAT, acceptance criteria, migration/seed, rollout/rollback, training, 16-sprint roadmap và release checklist.

## 6. Quy tắc làm việc trong từng chat gói

Tên chat đề xuất:
- `[PK00] Tổng quan & quản trị dự án`
- `[PK01] Kiến trúc tổng thể`
- ...
- `[PK12] Kiểm thử & triển khai`

Prompt mở đầu dùng cho từng chat:

> Đây là chat chuyên trách gói **[MÃ GÓI – TÊN GÓI]** của dự án School ERP SaaS trong Project “dự án ứng dụng”. Hãy sử dụng tài liệu `SCHOOL_ERP_PROJECT_MASTER.md` làm baseline. Chỉ tập trung đặc tả gói này; không tự ý thay đổi quyết định kiến trúc đã chốt. Nếu phát hiện mâu thuẫn hoặc cần thay đổi quyết định chung, hãy nêu rõ tác động và đề xuất cập nhật PK00/ADR trước. Mọi nội dung cần có yêu cầu, quy tắc nghiệp vụ, mô hình dữ liệu, API, phân quyền, bảo mật, kiểm thử và tiêu chí nghiệm thu phù hợp với phạm vi gói. Quản lý thay đổi theo phiên bản và ghi rõ nội dung mới/sửa/xóa.

## 7. Quy tắc quản lý phiên bản và thay đổi

Mỗi gói cần có:
- Mã gói và phiên bản: ví dụ `PK02 v1.0`.
- Trạng thái: Draft / In Review / Approved / Implemented / Deprecated.
- Ngày cập nhật và tóm tắt thay đổi.
- Quyết định đã chốt và vấn đề còn mở.
- Phụ thuộc và ảnh hưởng đến các gói khác.

Không ghi đè quyết định cũ mà không có change note. Khi một thay đổi ảnh hưởng nhiều gói, lập change request tại PK00, liệt kê gói bị ảnh hưởng và cập nhật đồng bộ.

## 8. Trạng thái khởi tạo

- Baseline Organization → nhiều School: đã xác định.
- Organization có một subdomain: đã xác định.
- Shared database/shared schema: đã xác định.
- Single account identity: đã xác định.
- Super Admin Support Mode: đã xác định về nguyên tắc, cần đặc tả chi tiết.
- Modular Monolith: định hướng đã xác định.
- Chi tiết ERD, permission catalog, API, UI, nghiệp vụ từng phân hệ: chưa hoàn tất, cần phát triển trong các gói tương ứng.

## 9. Quy tắc ưu tiên khi có mâu thuẫn

1. Yêu cầu mới được người dùng xác nhận rõ ràng.
2. Quyết định kiến trúc đã được phê duyệt trong PK00/ADR.
3. Baseline trong tài liệu này.
4. Đề xuất hoặc giả định của trợ lý.

Nếu thiếu dữ kiện, ghi `OPEN QUESTION` thay vì tự suy đoán thành quyết định.


## 10. PK00 — Yêu cầu mới đã xác nhận: Service Package, Dynamic Menu & RBAC

**Ngày xác nhận:** 2026-11-13  
**Trạng thái:** Accepted về định hướng kiến trúc; chi tiết kỹ thuật đang được đặc tả.  
**Phạm vi ảnh hưởng:** PK02, PK03, PK04, PK09, PK10, PK11, PK12.

### 10.1. Quyết định đã xác nhận

1. **Gói dịch vụ:** Mỗi Organization có một gói chính và có thể có nhiều gói chức năng bổ sung (Add-on). Entitlement của Organization là hợp nhất các Feature được cấp từ gói chính và các Add-on còn hiệu lực.
2. **Role:** Organization được tự tạo Role tùy chỉnh. Permission được gán cho Role phải nằm trong phạm vi Feature entitlement mà Organization được sử dụng.
3. **Quyền User:** Phiên bản đầu tiên không cấp permission trực tiếp cho từng User. Quyền hiệu lực đến từ một hoặc nhiều Role được gán cho User thông qua membership.
4. **Menu:** Super Admin định nghĩa menu nền. Organization được tùy chỉnh menu nền và tạo menu riêng. Menu riêng chỉ được trỏ đến route/feature hợp lệ mà Organization được cấp; menu không thay thế kiểm tra authorization ở backend.

### 10.2. Nguyên tắc nghiệp vụ và bảo mật

- Package, Feature, Permission và Menu là các khái niệm riêng biệt; không đồng nhất Feature với Permission.
- Gói dịch vụ cấp quyền sử dụng Feature cho Organization, không tự động cấp quyền thao tác cho mọi User.
- Role/Permission quyết định hành động; Data Scope giới hạn tập dữ liệu mà hành động được phép tác động.
- Một User có thể có nhiều Role trong cùng phạm vi membership; quyền hiệu lực là hợp các quyền hợp lệ từ các Role, sau đó tiếp tục bị giới hạn bởi entitlement, trạng thái tài khoản/membership và data scope.
- Không cho Organization tạo Permission tùy ý trong phiên bản đầu; Organization lựa chọn Permission từ Permission Catalog do nền tảng công bố và chỉ trong phạm vi Feature entitlement.
- Không cho phép menu tùy chỉnh vượt qua entitlement hoặc RBAC. Route/API phải được backend bảo vệ độc lập.
- Khi subscription/add-on hết hạn, bị tạm ngưng hoặc thu hồi, entitlement tương ứng phải mất hiệu lực theo thời điểm hiệu lực đã quy định; cần invalidation cache và audit.
- Gói đã được gán cần có chính sách versioning/snapshot để thay đổi catalog hoặc package template không âm thầm thay đổi quyền đã bán/cấp cho Organization.
- Mọi thao tác tạo/sửa/gán/thu hồi package, subscription, feature, role, permission và menu phải có audit phù hợp.

### 10.3. Danh sách yêu cầu (Requirement Register)

| Mã yêu cầu | Nội dung | Ưu tiên | Gói chính | Gói liên quan |
|---|---|---|---|---|
| REQ-PK00-NEW-001 | Một Organization có một gói chính và nhiều Add-on | Must | PK02 | PK03, PK04, PK09, PK10 |
| REQ-PK00-NEW-002 | Super Admin quản lý Feature Catalog và Permission Catalog | Must | PK03 | PK04, PK09, PK10 |
| REQ-PK00-NEW-003 | Super Admin cấp/gia hạn/tạm ngưng/thu hồi gói chính và Add-on cho Organization | Must | PK02 | PK03, PK04, PK09, PK11 |
| REQ-PK00-NEW-004 | Hợp nhất entitlement từ gói chính và Add-on còn hiệu lực | Must | PK03 | PK02, PK04, PK09 |
| REQ-PK00-NEW-005 | Organization tạo Role tùy chỉnh và gán Permission được phép | Must | PK03 | PK04, PK09, PK10 |
| REQ-PK00-NEW-006 | User nhận quyền thông qua một hoặc nhiều Role; không cấp quyền trực tiếp trong phiên bản đầu | Must | PK03 | PK04, PK09, PK10 |
| REQ-PK00-NEW-007 | Super Admin quản lý menu nền; Organization tùy chỉnh và tạo menu riêng | Must | PK10 | PK03, PK04, PK09 |
| REQ-PK00-NEW-008 | Menu chỉ hiển thị khi Organization có entitlement và User có quyền phù hợp | Must | PK10 | PK03, PK09 |
| REQ-PK00-NEW-009 | Backend authorization kiểm tra subscription, feature, permission và data scope | Must | PK03 | PK01, PK09, PK12 |
| REQ-PK00-NEW-010 | Audit, cache invalidation và hiệu lực tức thời/đúng hạn khi thay đổi entitlement và RBAC | Must | PK11 | PK02, PK03, PK09, PK12 |
| REQ-PK00-NEW-011 | Quản lý phiên bản package/catalog và chính sách áp dụng thay đổi cho subscription hiện hữu | Should | PK02 | PK03, PK04, PK11 |
| REQ-PK00-NEW-012 | Cho phép Organization cấu hình thứ tự, nhãn, icon, nhóm và trạng thái hiển thị menu; menu riêng chỉ trỏ đến đích được đăng ký | Should | PK10 | PK03, PK04 |

### 10.4. ADR đề xuất

#### ADR-001 — Mô hình gói dịch vụ chính + Add-on

- **Trạng thái:** Accepted (quyết định mô hình); chi tiết billing/versioning còn mở.
- **Bối cảnh:** Một Organization cần mua gói nền tảng và bổ sung riêng từng nhóm chức năng.
- **Quyết định:** Một subscription gói chính tại một thời điểm, kết hợp nhiều subscription Add-on. Entitlement được tính từ các gói đang hiệu lực, có quy tắc xử lý trùng Feature và ngày bắt đầu/kết thúc.
- **Hệ quả:** Cần quản lý subscription item, lịch sử thay đổi, trạng thái, hiệu lực, giới hạn sử dụng và audit. Không gắn trực tiếp danh sách Feature cố định vào Organization.
- **Vấn đề mở:** Có cho phép đổi gói chính giữa chu kỳ; prorate/hoàn phí; xử lý Feature trùng giữa gói chính và Add-on; thời gian grace period.

#### ADR-002 — RBAC theo Role tùy chỉnh, không có User Permission Override ở phiên bản đầu

- **Trạng thái:** Accepted.
- **Bối cảnh:** Cần linh hoạt phân quyền tại từng Organization nhưng giữ quyền kiểm soát tập trung đối với Permission Catalog.
- **Quyết định:** Nền tảng quản lý Permission Catalog theo `resource:action`; Organization tạo Role và gán Permission được entitlement cho phép; User nhận một hoặc nhiều Role thông qua membership. Không hỗ trợ quyền trực tiếp/ngoại lệ permission ở cấp User trong phiên bản đầu.
- **Hệ quả:** Cần quản lý ownership/scope của Role, unique constraint phù hợp theo tenant, role assignment, data scope, cache quyền và audit. Cần phân biệt Role hệ thống được seed/managed và Role tùy chỉnh của Organization.
- **Vấn đề mở:** Quy tắc sửa/xóa Role mặc định; cách xử lý User đang giữ Role bị xóa; quyền quản trị Role tối thiểu của Organization Admin.

#### ADR-003 — Menu nền của nền tảng kết hợp menu tùy chỉnh theo Organization

- **Trạng thái:** Accepted.
- **Bối cảnh:** Nền tảng cần trải nghiệm điều hướng thống nhất nhưng từng Organization có thể sắp xếp và bổ sung lối vào nghiệp vụ riêng.
- **Quyết định:** Super Admin quản lý menu nền; Organization có lớp cấu hình override (ẩn/hiện, thứ tự, nhãn/icon nếu được cho phép) và có thể tạo menu riêng. Mỗi menu item phải tham chiếu Feature/route/permission đã đăng ký; không cho menu tùy chỉnh tạo quyền truy cập mới.
- **Hệ quả:** Cần menu catalog/version, organization menu configuration, validation route/feature, API trả menu đã được lọc theo entitlement và quyền User. Backend vẫn kiểm tra quyền tại endpoint.
- **Vấn đề mở:** Phạm vi tùy chỉnh nhãn/icon; menu đa cấp; route registry; menu item trỏ đến external URL; quyền tạo menu riêng theo Role nào.

### 10.5. Đồng bộ phạm vi theo gói

**PK02 — Đa tổ chức & đa trường**
- Mô hình subscription của Organization: một gói chính + nhiều Add-on.
- Quy trình cấp, gia hạn, đổi, tạm ngưng, thu hồi và hết hạn subscription.
- Quy tắc tenant ownership; Organization không tự nâng entitlement ngoài quyền mua/cấp.

**PK03 — Tài khoản & phân quyền**
- Feature entitlement là lớp kiểm tra trước RBAC.
- Permission Catalog chuẩn `resource:action` (ví dụ `attendance:read`, `grade:input`, `fee:collect`).
- Role hệ thống và Role tùy chỉnh theo Organization; user-role assignment qua membership.
- Không có user-level permission override trong phiên bản đầu.
- Data Scope là lớp giới hạn dữ liệu sau khi xác định permission.

**PK04 — Cơ sở dữ liệu**
- Đặc tả ERD, khóa ngoại, unique constraints, index, soft delete và audit cho package, package version, package feature, subscription, entitlement, feature, menu, role, permission, role-permission, user-role và scope.
- Tenant-scoped tables phải mang `organization_id`/`school_id` phù hợp, có composite FK và RLS theo baseline.
- Xác định snapshot/versioning để thay đổi package template không làm thay đổi âm thầm subscription đang hoạt động.

**PK09 — API & tích hợp**
- API quản trị package/catalog và subscription/add-on.
- API đọc entitlement hiệu lực của Organization.
- API quản lý Role, Permission và assignment.
- API lấy menu đã lọc theo entitlement + RBAC; API quản lý menu customization.
- Chuẩn lỗi 401/403/404; idempotency cho thao tác cấp/gia hạn/thu hồi; audit events; cache invalidation.

**PK10 — Giao diện & ứng dụng**
- Platform Admin: Feature Catalog, Permission Catalog, Package Builder, Package Assignment/Subscription Management, Menu Builder.
- Organization Admin: xem gói và entitlement, quản lý Role/Permission, cấu hình menu nền và menu riêng.
- Ứng dụng người dùng: render menu động từ API; xử lý trạng thái không có quyền; không xem việc ẩn menu là biện pháp bảo mật.
- Menu đa cấp, thứ tự, icon, nhãn và responsive navigation cần được đặc tả.

**PK01/PK11/PK12 — Phụ thuộc liên quan**
- PK01: authorization middleware/policy, tenant context, cache strategy.
- PK11: audit log, bảo mật, cache invalidation, observability và vận hành subscription.
- PK12: test entitlement, RBAC, data scope, menu filtering, tenant isolation, hết hạn/thu hồi và race conditions.

### 10.6. Mô hình dữ liệu định hướng (chưa phải ERD được duyệt)

Các nhóm thực thể cần đặc tả trong PK04:

- Catalog: `features`, `permissions`, `menus`, `menu_routes` (hoặc route registry tương đương), `feature_permissions`, `feature_menus`.
- Package: `service_packages`, `service_package_versions`, `service_package_features`.
- Subscription: `organization_subscriptions` hoặc `subscriptions`, `subscription_items` cho gói chính/Add-on, `subscription_entitlements` nếu cần snapshot/materialization.
- RBAC: `roles`, `role_permissions`, `user_roles`, `role_scopes`.
- Menu tenant: `organization_menu_items` hoặc `organization_menu_configs`.
- Audit: dùng audit framework chung, lưu actor, tenant, action, before/after, reason và timestamp.

Tên bảng trên là tên định hướng để PK04 đánh giá; chưa chốt schema cuối cùng. `user_permission_overrides` không nằm trong phạm vi phiên bản đầu.

### 10.7. Tiêu chí nghiệm thu cấp dự án

1. Super Admin tạo được gói chính và Add-on, khai báo Feature được bao gồm.
2. Organization được cấp một gói chính và nhiều Add-on; hệ thống tính đúng entitlement đang hiệu lực.
3. Organization không thể gán Role permission ngoài entitlement.
4. User có nhiều Role và quyền hiệu lực được tổng hợp đúng; không tồn tại luồng cấp permission trực tiếp cho User trong phiên bản đầu.
5. Organization có thể tùy chỉnh menu nền và tạo menu riêng trong giới hạn cho phép.
6. Menu API không trả về mục không thuộc entitlement hoặc không có quyền phù hợp.
7. Gọi trực tiếp API không có quyền vẫn bị backend từ chối, kể cả khi biết URL.
8. Hết hạn/thu hồi Add-on làm mất quyền Feature tương ứng mà không ảnh hưởng Feature vẫn được cấp bởi gói chính hoặc Add-on khác.
9. Mọi thay đổi quan trọng có audit và cache được làm mới theo chính sách.
10. Kiểm thử tenant isolation chứng minh Organization không thể đọc/sửa subscription, Role, menu hoặc dữ liệu của Organization khác.

### 10.8. Open Questions cần xử lý ở các gói chuyên trách

- Có cho phép nhiều Add-on chứa cùng một Feature không? Nếu có, entitlement có hiệu lực đến khi tất cả nguồn cấp đều hết hiệu lực hay theo quy tắc khác?
- Có cho phép đổi gói chính giữa chu kỳ và cần prorate không?
- Gói/package version thay đổi có áp dụng ngay cho subscription hiện tại hay chỉ cho subscription mới/gia hạn?
- Organization có được đổi nhãn/icon của menu nền không? Có được ẩn menu bắt buộc không?
- Menu riêng được trỏ đến route nội bộ đã đăng ký hay có hỗ trợ external URL?
- Role mặc định do nền tảng cung cấp có được Organization chỉnh sửa hay chỉ nhân bản thành Role riêng?
- Permission có hỗ trợ điều kiện thuộc tính (ABAC) hay phiên bản đầu chỉ dùng RBAC + Data Scope?

### 10.9. Change Log

| Phiên bản | Ngày | Thay đổi |
|---|---|---|
| 1.1 | 2026-11-13 | Ghi nhận 4 quyết định đã xác nhận; bổ sung REQ-PK00-NEW-001 đến 012; đề xuất ADR-001 đến ADR-003; đồng bộ phạm vi PK02/03/04/09/10 và phụ thuộc PK01/11/12. |


## 11. Tiến độ đặc tả Service Package, RBAC & Dynamic Menu

**Ngày cập nhật:** 2026-11-13  
**Trạng thái:** Các quyết định kiến trúc đã được xác nhận; đặc tả kỹ thuật v0.1 đang ở Draft for Review.

### 11.1. Các tài liệu đã khởi tạo
- `PK03_TAI_KHOAN_VA_PHAN_QUYEN_v0.1.md`: entitlement-aware Custom RBAC.
- `PK04_ERD_SERVICE_PACKAGE_RBAC_DYNAMIC_MENU_v0.1.md`: ERD, data dictionary, constraints, migration order.
- `PK09_API_VA_TICH_HOP_v0.1.md`: API surface và authorization contract.
- `PK10_GIAO_DIEN_VA_UNG_DUNG_v0.1.md`: UI scope cho Platform Admin, Organization Admin và end users.

### 11.2. Trạng thái
| Gói | Trạng thái | Nội dung |
|---|---|---|
| PK03 | Draft v0.1 | Feature entitlement, Custom RBAC, Role assignment, Data Scope |
| PK04 | Draft v0.1 | ERD và data dictionary cho package, subscription, RBAC, menu, audit |
| PK09 | Draft v0.1 | API catalog và authorization pipeline |
| PK10 | Draft v0.1 | Package Builder, Role Management, Menu Builder, end-user navigation |

### 11.3. Quy tắc thực hiện tiếp
- Không triển khai migration production trước khi xử lý các OPEN QUESTION có ảnh hưởng đến constraints và membership.
- Package version đã publish là immutable.
- Entitlement được tính từ subscription item và package version features; cache/projection không là nguồn sự thật.
- Menu không là ranh giới bảo mật; backend kiểm tra authorization trên từng API/action.
- Không cấp permission trực tiếp cho User trong MVP.
- Các bảng tenant-scoped tuân thủ composite FK, RLS và `SET LOCAL` theo baseline mục 4.

### 11.4. OPEN QUESTIONS ưu tiên chốt
1. Mô hình membership Organization/School và khóa ngoại của Role assignment.
2. Quy tắc primary subscription không chồng lấn thời gian.
3. Package version mới áp dụng cho subscription hiện tại theo cách nào.
4. Xử lý Add-on trùng Feature.
5. Menu policy ANY/ALL permissions và menu bắt buộc.
6. Role template nền và quyền nhạy cảm cần approval hay không.
7. PostgreSQL version mục tiêu và cơ chế exclusion constraint.
