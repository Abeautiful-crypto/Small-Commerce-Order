package api

// imageExt 按文件头嗅探结果给出落盘扩展名；只接受四种常见图片格式。
// 不信任客户端文件名后缀，避免把 .html/.svg 之类可执行内容当图片存进站点目录。
func imageExt(contentType string) (string, bool) {
	switch contentType {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	case "image/gif":
		return ".gif", true
	}
	return "", false
}
