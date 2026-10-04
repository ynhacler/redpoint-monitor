# 由 api/openapi.yaml 生成 Web 的 TypeScript 请求类型（设计 19.0.1）：web/src/api.gen.ts。
# 只用 Ruby 标准库，不引入生成器依赖。生成文件提交到仓库；CI 用 --check 确认与契约一致。
#
# 用法：ruby scripts/gen-api-types.rb [--check]
#
# 输出：
#   components.schemas 中的每个模型 → export interface / export type
#   paths 中的每个操作 → Paths['/路径']['get' | 'post' | …] = { params?, query?, body?, response }
#     params 为路径参数，query 为查询参数，body 为 JSON 请求体，response 为 2xx 的 JSON 响应（无内容时为 void）
require "yaml"
Encoding.default_external = Encoding::UTF_8 # 不依赖系统区域设置（CI、macOS 自带的 Ruby）

ROOT = File.expand_path("..", __dir__)
SPEC = File.join(ROOT, "api/openapi.yaml")
OUT = File.join(ROOT, "web/src/api.gen.ts")
METHODS = %w[get post put patch delete].freeze

$doc = YAML.load_file(SPEC)

def resolve(o)
  return o unless o.is_a?(Hash) && o["$ref"]
  o["$ref"].delete_prefix("#/").split("/").reduce($doc) { |h, k| h.fetch(k) }
end

def ref_name(ref)
  ref.split("/").last
end

def ident?(k)
  k.match?(/\A[A-Za-z_$][A-Za-z0-9_$]*\z/)
end

# 一行 JSDoc；描述中的 */ 会提前结束注释，替换掉
def doc_comment(desc, indent)
  return "" if desc.nil? || desc.to_s.strip.empty?
  text = desc.to_s.strip.gsub("*/", "*\\/").gsub(/\s*\n\s*/, " ")
  "#{indent}/** #{text} */\n"
end

def literal(v)
  v.is_a?(String) ? "'#{v.gsub("'", "\\\\'")}'" : v.to_s
end

def scalar(t)
  case t
  when "integer", "number" then "number"
  when "string" then "string"
  when "boolean" then "boolean"
  when "null" then "null"
  when "array" then "unknown[]"
  else "unknown"
  end
end

# 把一个 schema 转为 TypeScript 类型表达式
def ts(s, indent = "")
  return "unknown" if s.nil?
  return ref_name(s["$ref"]) if s["$ref"]
  return s["enum"].map { |v| literal(v) }.join(" | ") if s["enum"]
  return literal(s["const"]) if s.key?("const")
  return s["oneOf"].map { |x| ts(x, indent) }.join(" | ") if s["oneOf"]
  return s["anyOf"].map { |x| ts(x, indent) }.join(" | ") if s["anyOf"]
  return s["allOf"].map { |x| ts(x, indent) }.join(" & ") if s["allOf"]

  t = s["type"]
  if t.is_a?(Array)
    others = t - ["null"]
    base = others.size == 1 ? ts(s.merge("type" => others[0]), indent) : others.map { |x| scalar(x) }.join(" | ")
    return t.include?("null") ? "#{base} | null" : base
  end
  case t
  when "array" then
    item = ts(s["items"], indent)
    item.match?(/[|&]/) ? "(#{item})[]" : "#{item}[]"
  when "object", nil
    props = s["properties"]
    ap = s["additionalProperties"]
    if props.nil? || props.empty?
      return ap.is_a?(Hash) ? "Record<string, #{ts(ap, indent)}>" : (t == "object" ? "Record<string, unknown>" : "unknown")
    end
    object(props, s["required"] || [], indent)
  else scalar(t)
  end
end

def object(props, required, indent)
  inner = indent + "  "
  body = props.map do |k, v|
    v ||= {}
    key = ident?(k) ? k : "'#{k}'"
    opt = required.include?(k) ? "" : "?"
    doc_comment(v["description"], inner) + "#{inner}#{key}#{opt}: #{ts(v, inner)}\n"
  end.join
  "{\n#{body}#{indent}}"
end

def json_schema(content)
  return nil unless content
  c = content["application/json"]
  c && c["schema"]
end

out = +<<~TS
  // 由 scripts/gen-api-types.rb 根据 api/openapi.yaml 生成，不要手改（设计 19.0.1）。
  // 修改接口时先改 api/openapi.yaml，再运行 make api-types。
  /* eslint-disable */

TS

$doc.dig("components", "schemas").each do |name, s|
  out << doc_comment(s["description"], "")
  expr = ts(s)
  if s["type"] == "object" && s["properties"] && !s["allOf"]
    out << "export interface #{name} #{expr}\n\n"
  else
    out << "export type #{name} = #{expr}\n\n"
  end
end

out << "/** 每个接口的路径参数、查询参数、请求体与成功响应 */\nexport interface Paths {\n"
$doc["paths"].each do |path, item|
  common = (item["parameters"] || []).map { |p| resolve(p) }
  ops = METHODS.select { |m| item[m] }
  next if ops.empty?
  out << "  '#{path}': {\n"
  ops.each do |m|
    op = item[m]
    params = common + (op["parameters"] || []).map { |p| resolve(p) }
    out << doc_comment(op["summary"], "    ")
    out << "    #{m}: {\n"
    %w[path query].each do |where|
      ps = params.select { |p| p["in"] == where }
      next if ps.empty?
      props = ps.to_h { |p| [p["name"], (p["schema"] || {}).merge("description" => p["description"])] }
      req = ps.select { |p| p["required"] }.map { |p| p["name"] }
      key = where == "path" ? "params" : "query"
      out << "      #{key}#{req.empty? ? "?" : ""}: #{object(props, req, "      ")}\n"
    end
    if (rb = op["requestBody"])
      rb = resolve(rb)
      if (schema = json_schema(rb["content"]))
        out << "      body#{rb["required"] ? "" : "?"}: #{ts(schema, "      ")}\n"
      end
    end
    code, resp = (op["responses"] || {}).find { |c, _| c.to_s.start_with?("2") }
    resp = resolve(resp) if resp
    schema = resp && json_schema(resp["content"])
    type = if schema then ts(schema, "      ")
           elsif resp && resp["content"] then "unknown" # 非 JSON（CSV、二进制下载），不经过 request()
           else "void"
           end
    out << "      response: #{type}\n"
    out << "    }\n"
  end
  out << "  }\n"
end
out << "}\n"

if ARGV.include?("--check")
  current = File.exist?(OUT) ? File.read(OUT) : ""
  abort "web/src/api.gen.ts 与 api/openapi.yaml 不一致：运行 make api-types 后提交" unless current == out
  puts "api.gen.ts 与契约一致"
else
  File.write(OUT, out)
  puts "已生成 #{OUT.delete_prefix(ROOT + "/")}"
end
