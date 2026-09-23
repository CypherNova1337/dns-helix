# DNS-Helix

Guesses subdomain names that probably exist, then checks which ones really do.

![license](https://img.shields.io/badge/license-MIT-blue?style=flat-square)
![go](https://img.shields.io/badge/go-1.24%2B-00ADD8?style=flat-square)
![release](https://img.shields.io/badge/release-v1.0.2-brightgreen?style=flat-square)

## What it does

Subdomain enumeration usually finds the obvious names — the ones in certificate
logs, in search results, in somebody's old scrape. Those are the names everyone
already has.

The interesting hosts are often the ones nobody published. If a company runs
`api.example.com`, there is a fair chance they also run `api-dev`, `api2`,
`api-staging`, `api.eu` or `dev-api`. Nothing lists those anywhere. They exist
because someone needed a second one and followed the same naming habit.

DNS-Helix builds those names and tests them. Give it the subdomains you already
know and it generates variations — inserting words, changing numbers, adding
environment and region prefixes — then resolves the whole set at speed to see
which ones answer.

It is the step *after* your usual enumeration, not a replacement for it. Feed it
what you found; it gives you back the neighbours.

## Why you'd use it

- **Generates candidates, doesn't just resolve a list** you had to build
  yourself.
- **Works straight out of the box.** A curated permutation wordlist and a
  vetted resolver list are compiled into the binary, so a scan runs from any
  directory with no extra files.
- **Resumable.** Interrupt a long run and it picks up where it stopped instead
  of starting over.
- **Doesn't waste queries.** Overlapping permutations are looked up once.
- **Retries before giving up**, so one flaky resolver doesn't turn a live host
  into a false negative.

## Install

```bash
go install github.com/CypherNova1337/dns-helix@latest
```

Or from a checkout:

```bash
git clone https://github.com/CypherNova1337/dns-helix
cd dns-helix
go build
```

Needs Go 1.24 or newer.

## Usage

Pipe in the subdomains you already have:

```bash
cat subdomains.txt | dns-helix
```

Results land in `resolved_subdomains.txt`.

**From a file instead of stdin**

```bash
dns-helix -s subdomains.txt -o found.txt
```

**Check the seeds are real first**

```bash
dns-helix -s subdomains.txt -pre-validate
```

Worth doing when your input came from a scrape. Dead seeds generate thousands of
permutations of a host that never existed.

**Ease off a fragile network**

```bash
dns-helix -s subdomains.txt -t 25 -l 200
```

**Resume after an interruption**

```bash
dns-helix -s subdomains.txt -resume run.state
# Ctrl+C, then run the same command again
```

**Use your own lists**

```bash
dns-helix -s subdomains.txt -w custom_words.txt -r my_resolvers.txt
```

## Options

| Flag | Default | What it does |
|---|---|---|
| `-s` | stdin | File of seed subdomains, one per line |
| `-o` | `resolved_subdomains.txt` | Where results go |
| `-w` | built-in | Permutation wordlist |
| `-r` | built-in | DNS resolver list |
| `-t` | `100` | Concurrent resolving threads |
| `-l` | `1000` | Max queries per second (`0` = unlimited) |
| `-retries` | `2` | Attempts per name before calling it dead |
| `-pre-validate` | off | Check seeds resolve before generating from them |
| `-resume` | — | Resume file for continuing a scan |
| `-version` | — | Print version and exit |

## Good to know

- **Garbage in, garbage out.** Permutations are built from your seeds, so a good
  seed list matters more than any flag here.
- **Your ISP's resolver will not cope.** The bundled resolver list exists
  because a home router falls over well before 1000 queries per second. Keep it
  unless you have something better.
- **A name that resolves isn't necessarily a live service.** Wildcard DNS makes
  everything answer. Probe the results with `httpx` or similar before getting
  excited.
- **This is loud.** Thousands of DNS queries is not a subtle thing to do.

## Authorised use

Run it against domains you own or that are in scope for an engagement or bounty
programme you're part of.

## License

MIT — see [LICENSE](LICENSE).
