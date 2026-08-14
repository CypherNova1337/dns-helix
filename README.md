# DNS-Helix 🧬

<pre>
__/\\\\\\\\\\\\_____/\\\\\_____/\\\_____/\\\\\\\\\\\______________/\\\________/\\\__/\\\\\\\\\\\\\\\__/\\\______________/\\\\\\\\\\\__/\\\_______/\\\_        
 _\/\\\////////\\\__\/\\\\\\___\/\\\___/\\\/////////\\\___________\/\\\_______\/\\\_\/\\\///////////__\/\\\_____________\/////\\\///__\///\\\___/\\\/__       
  _\/\\\______\//\\\_\/\\\/\\\__\/\\\__\//\\\______\///____________\/\\\_______\/\\\_\/\\\_____________\/\\\_________________\/\\\_______\///\\\\\\/____      
   _\/\\\_______\/\\\_\/\\\//\\\_\/\\\___\////\\\___________________\/\\\\\\\\\\\\\\\_\/\\\\\\\\\\\_____\/\\\_________________\/\\\_________\//\\\\______     
    _\/\\\_______\/\\\_\/\\\\//\\\\/\\\______\////\\\________________\/\\\/////////\\\_\/\\\///////______\/\\\_________________\/\\\__________\/\\\\______    
     _\/\\\_______\/\\\_\/\\\_\//\\\/\\\_________\////\\\_____________\/\\\_______\/\\\_\/\\\_____________\/\\\_________________\/\\\__________/\\\\\\_____   
      _\/\\\_______/\\\__\/\\\__\//\\\\\\__/\\\______\//\\\____________\/\\\_______\/\\\_\/\\\_____________\/\\\_________________\/\\\________/\\\////\\\___  
       _\/\\\\\\\\\\\\/___\/\\\___\//\\\\\_\///\\\\\\\\\\\/_____________\/\\\_______\/\\\_\/\\\\\\\\\\\\\\\_\/\\\\\\\\\\\\\\\__/\\\\\\\\\\\__/\\\/___\///\\\_ 
        _\////////////_____\///_____\/////____\///////////_______________\///________\///__\///////////////__\///////////////__\///////////__\///_______\///__
</pre>
<p align="right">by CypherNova</p>

A lightning-fast DNS permutation scanner and resolver, built in Go for speed and efficiency.

---

## Features

- **High-Speed, Concurrent DNS Resolution:** Utilizes goroutines to resolve thousands of domains per second.
- **Advanced Permutation Generation:** Creates new potential subdomains by inserting words, modifying numbers, and adding common prefixes (dev, staging, regions).
- **Batteries Included:** A curated permutation wordlist and a high-quality resolver list are compiled into the binary, so a scan runs from any directory with no extra files. On-disk lists always override the built-ins.
- **Seed Validation (`--pre-validate`):** Optionally pre-scans the initial list of domains to ensure permutations are only generated for valid, resolvable seeds.
- **Session Resumption (`--resume`):** On interruption (`Ctrl+C`) it saves every seed whose permutations were not yet processed, so a resumed run regenerates and completes the remaining work rather than losing it.
- **Duplicate-Aware:** Overlapping permutations are looked up only once, so no DNS query is ever wasted on a name that has already been tried.
- **Retries (`--retries`):** Each candidate is retried against a fresh random resolver before being declared dead, avoiding false negatives from a single slow or flaky upstream.
- **`anew`-style Output:** Intelligently merges new results with existing ones in the output file, creating a unique, sorted list every time.
- **Rate-Limiting (`-l`):** Configurable rate-limiting to prevent network blocking and ensure stability (`0` = unlimited).
- **Piped Input:** Fully supports piped input from other command-line tools.

## Installation

To install `dns-helix`, you'll need Go installed on your machine. Then, run the following command:

```bash
go install -v github.com/CypherNova1337/dns-helix@latest
```

## Usage

Here are some examples of how to use `dns-helix`.

**Zero-config Scan:**
The wordlist and resolver list are compiled into the binary, so a scan works
from any directory with nothing but a list of seeds — no files to copy around:
```bash
dns-helix -s all_subdomains.txt -o results.txt
```

**Basic Scan (custom wordlist):**
```bash
dns-helix -s path/to/subdomains.txt -w path/to/wordlist.txt -o path/to/results.txt
```

**Advanced Scan with Pre-Validation (Recommended):**
This is the most efficient method for large, potentially noisy seed lists.
```bash
dns-helix -s path/to/subdomains.txt -w path/to/wordlist.txt -r path/to/resolvers.txt -o path/to/results.txt --pre-validate -l 1000 -t 200
```

**Resume an Interrupted Scan:**
```bash
dns-helix --resume path/to/resume-file.log -r path/to/resolvers.txt -o path/to/results.txt
```

### Flag Breakdown
| Flag | Description | Default |
|---|---|---|
| `-s` | Path to the subdomains file (optional, reads from stdin). | |
| `-w` | Path to the wordlist file. Falls back to the built-in list if absent. | `words.txt` |
| `-r` | Path to the DNS resolvers file. Falls back to the built-in list if absent. | `recommended_resolvers.txt` |
| `-o` | Path to the output file. | `resolved_subdomains.txt`|
| `-t` | Number of concurrent DNS resolving threads. | `100` |
| `-l` | Max queries per second to send (`0` = unlimited). | `1000` |
| `--retries` | Resolution attempts per domain before giving up (min 1). | `2` |
| `--pre-validate` | Pre-validate base domains before generating permutations. | `false`|
| `--resume` | Path to a resume file to continue a previous scan. | |


## The Resolver List

While `dns-helix` will work with any resolver list, the quality of your resolvers is the **single most important factor** for achieving high performance.

This repository includes `recommended_resolvers.txt`, a curated list of globally recognized, high-performance public DNS servers. Using this small, curated list is highly recommended. Many publicly available resolver lists contain thousands of IPs, which is often unnecessary and counterproductive. These large lists are typically filled with slow, unreliable, or offline servers that cause the scan to time out and slow down dramatically.

For DNS discovery, **quality is far more important than quantity**.

```bash
dns-helix -s path/to/subdomains.txt -w path/to/wordlist.txt -r recommended_resolvers.txt -o path/to/results.txt
```

Both `recommended_resolvers.txt` and `words.txt` are also **compiled into the
binary**, so if either file is missing from the working directory the scan
transparently falls back to the built-in copy. An on-disk file — the default
name in the current directory, or a path you pass with `-w` / `-r` — always
takes precedence.

## The Wordlist

`words.txt` is a curated **permutation** wordlist, not a brute-force one. Because
every word is inserted at each position of every seed, the goal is a high
hit-rate per query: environment and lifecycle names (`dev`, `staging`, `uat`,
`canary`), infrastructure and networking (`internal`, `vpn`, `gateway`, `edge`,
`lb`), cloud and orchestration (`k8s`, `ecs`, `lambda`, `node`), data stores,
auth/identity, storage/CDN, mail, CI/CD, monitoring, security, and common
service and regional terms. This keeps the generated candidate set focused on
names that actually front real hosts instead of exploding your query count.
