# OpenAPI 契约的基本检查（设计 19.0.1、19.0.2）：能被严格的 YAML 解析器解析；
# 返回 items 的 GET 接口必须同时声明 next_cursor。完整的响应与契约一致性校验见 TODO(A0)。
require "yaml"
doc = YAML.load_file(ARGV[0] || "api/openapi.yaml")
bad = []
doc["paths"].each do |path, ops|
  schema = ops.dig("get", "responses", "200", "content", "application/json", "schema") or next
  props = schema["properties"] || {}
  bad << path if props.key?("items") && !props.key?("next_cursor")
end
abort "列表接口缺少 next_cursor（设计 19.0.2）：#{bad.join(', ')}" unless bad.empty?
puts "OpenAPI 检查通过（#{doc["paths"].size} 个路径）"
