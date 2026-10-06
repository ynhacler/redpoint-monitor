# OpenAPI 契约的基本检查（设计 19.0.1、19.0.2）：能被严格的 YAML 解析器解析；
# 返回 items 的 GET 接口必须同时声明 next_cursor，且两者都列入 required（writeList 总是返回）。响应与契约的一致性由服务端测试校验（internal/server/contract_test.go）。
require "yaml"
doc = YAML.load_file(ARGV[0] || "api/openapi.yaml")
bad = []
loose = []
doc["paths"].each do |path, ops|
  schema = ops.dig("get", "responses", "200", "content", "application/json", "schema") or next
  props = schema["properties"] || {}
  next unless props.key?("items")
  bad << path unless props.key?("next_cursor")
  loose << path unless (%w[items next_cursor] - (schema["required"] || [])).empty?
end
abort "列表接口缺少 next_cursor（设计 19.0.2）：#{bad.join(', ')}" unless bad.empty?
abort "列表接口的 items、next_cursor 必须列入 required（总是返回）：#{loose.join(', ')}" unless loose.empty?
puts "OpenAPI 检查通过（#{doc["paths"].size} 个路径）"
