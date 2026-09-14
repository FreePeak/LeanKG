import os, sys

# Chained corpus: 100 .go files, 4 symbols each; mod_i's next{i} calls Handler{i+1}
# (mod_99 wraps to Handler0). Usage: python3 gen_corpus.py <out-dir>
OUT = sys.argv[1] if len(sys.argv) > 1 else "/tmp/ab-corpus"
os.makedirs(os.path.join(OUT, "src"), exist_ok=True)
tmpl = '''package pkg{i}

// Handler{i} processes request {i} and forwards to the next stage.
func Handler{i}(x int) int {{
	if x < 0 {{
		return next{i}(x)
	}}
	return x*{i} + helper{i}(x)
}}

// next{i} forwards to the next stage handler.
func next{i}(x int) int {{
	return Handler{nx}(x)
}}

// helper{i} internal helper.
func helper{i}(x int) int {{
	return x + {i}
}}

// Parser{i} parses config {i}.
type Parser{i} struct {{ Field{i} string }}
'''
for i in range(100):
    nx = i + 1 if i < 99 else 0
    with open(os.path.join(OUT, "src", f"mod{i}.go"), "w") as f:
        f.write(tmpl.format(i=i, nx=nx))
print("chained corpus written: 100 files")
