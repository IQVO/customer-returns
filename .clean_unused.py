import re, sys

def remove_symbol(src: str, name: str) -> str:
    lines = src.split("\n")
    out, i, removed = [], 0, False
    while i < len(lines):
        line = lines[i]
        # match: func [receiver] name( ... | type name ...
        if (re.match(r"(// [^\n]*)?\s*$", line) and i + 1 < len(lines)):
            pass
        header = lines[i] if not line.strip().startswith("//") else None
        j = i
        # skip leading comments for detection
        k = i
        while k < len(lines) and lines[k].strip().startswith("//"):
            k += 1
        if k < len(lines):
            decl = lines[k]
            is_func = re.match(r"func\s+(?:\([^)]*\)\s+)?" + re.escape(name) + r"\(", decl)
            is_type = re.match(r"type\s+" + re.escape(name) + r"\s", decl)
            if is_func or is_type:
                # find end: first line that is exactly "}" after decl (funcs/multi-line types), or the decl line itself (single-line type)
                if is_type and not decl.rstrip().endswith("{") and not decl.rstrip().endswith("("):
                    end = k
                else:
                    end = k
                    while end < len(lines) and lines[end] != "}":
                        end += 1
                # include leading comment lines
                start = i
                out.extend(lines[:start])
                out.extend(lines[end + 1:])
                return "\n".join(out)
        i += 1
    raise SystemExit(f"symbol not found: {name}")

targets = [
    ("internal/adapters/inbound/a2a/helpers_test.go", ["seededService", "must", "wireProblem", "wireEnvelope", "wire", "wantProblem"]),
    ("internal/adapters/inbound/http/handlers_test.go", ["newAPILogged"]),
    ("internal/adapters/inbound/kafka/helpers_test.go", ["fakeDLQWriter"]),
    ("cmd/api/config.go", ["errMissingSsccPrefix"]),
]
for path, names in targets:
    s = open(path).read()
    for n in names:
        s = remove_symbol(s, n)
    open(path, "w").write(s)
    print("cleaned", path)
print("done")
