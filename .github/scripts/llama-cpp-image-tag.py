import json, re, urllib.request
repo = "ggml-org/llama.cpp"
token = json.load(urllib.request.urlopen(f"https://ghcr.io/token?scope=repository:{repo}:pull"))["token"]
last, best = "", 0
while True:
    url = f"https://ghcr.io/v2/{repo}/tags/list?n=1000" + (f"&last={last}" if last else "")
    tags = json.load(urllib.request.urlopen(urllib.request.Request(url, headers={"Authorization": "Bearer " + token}))).get("tags") or []
    for t in tags:
        m = re.fullmatch(r"server-cuda13-b(\d+)", t)
        if m:
            best = max(best, int(m.group(1)))
    if len(tags) < 1000:
        break
    last = tags[-1]
print(f"b{best}")
