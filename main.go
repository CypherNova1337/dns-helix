package main

import (
	"bufio"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Bundled defaults compiled into the binary so a scan works from any directory,
// even when no words.txt / resolver list is present on disk. An on-disk file
// (default filename in the CWD, or one passed via -w / -r) always wins.
var (
	//go:embed words.txt
	embeddedWords string
	//go:embed recommended_resolvers.txt
	embeddedResolvers string
)

var (
	// attemptedDomains tracks every domain that has been queued for resolution
	// so the same name is never looked up twice, even when different mutation
	// strategies produce overlapping permutations.
	attemptedDomains sync.Map
)

const (
	colorCyan  = "\033[96m"
	colorReset = "\033[0m"

	// maxLabels caps how deep a generated permutation may go so we don't emit
	// absurdly long names that no resolver will ever answer.
	maxLabels = 6
)

// ASCII art banner
var banner = colorCyan + `
__/\\\\\\\\\\\\_____/\\\\\_____/\\\_____/\\\\\\\\\\\______________/\\\________/\\\__/\\\\\\\\\\\\\\\__/\\\______________/\\\\\\\\\\\__/\\\_______/\\\_
 _\/\\\////////\\\__\/\\\\\\___\/\\\___/\\\/////////\\\___________\/\\\_______\/\\\_\/\\\///////////__\/\\\_____________\/////\\\///__\///\\\___/\\\/__
  _\/\\\______\//\\\_\/\\\/\\\__\/\\\__\//\\\______\///____________\/\\\_______\/\\\_\/\\\_____________\/\\\_________________\/\\\_______\///\\\\\\/____
   _\/\\\_______\/\\\_\/\\\//\\\_\/\\\___\////\\\___________________\/\\\\\\\\\\\\\\\_\/\\\\\\\\\\\_____\/\\\_________________\/\\\_________\//\\\\______
    _\/\\\_______\/\\\_\/\\\\//\\\\/\\\______\////\\\________________\/\\\/////////\\\_\/\\\///////______\/\\\_________________\/\\\__________\/\\\\______
     _\/\\\_______\/\\\_\/\\\_\//\\\/\\\_________\////\\\_____________\/\\\_______\/\\\_\/\\\_____________\/\\\_________________\/\\\__________/\\\\\\_____
      _\/\\\_______/\\\__\/\\\__\//\\\\\\__/\\\______\//\\\____________\/\\\_______\/\\\_\/\\\_____________\/\\\_________________\/\\\________/\\\////\\\___
       _\/\\\\\\\\\\\\/___\/\\\___\//\\\\\_\///\\\\\\\\\\\/_____________\/\\\_______\/\\\_\/\\\\\\\\\\\\\\\_\/\\\\\\\\\\\\\\\__/\\\\\\\\\\\__/\\\/___\///\\\_
        _\////////////_____\///_____\/////____\///////////_______________\///________\///__\///////////////__\///////////////__\///////////__\///_______\///__
` + colorReset

// parseLines trims whitespace and drops blank/comment (#) lines, the shared
// filtering used for both on-disk files and the embedded defaults.
func parseLines(text string) []string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseLines(string(data)), nil
}

func partiateDomain(domain string) ([]string, string) {
	domain = strings.ToLower(domain)
	registeredDomain, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		parts := strings.Split(domain, ".")
		if len(parts) > 1 {
			if len(parts) > 2 {
				return parts[:len(parts)-2], strings.Join(parts[len(parts)-2:], ".")
			}
			return parts[:len(parts)-1], parts[len(parts)-1]
		}
		return []string{}, domain
	}
	subdomainPart := strings.TrimSuffix(domain, "."+registeredDomain)
	if subdomainPart == "" || subdomainPart == domain {
		return []string{}, registeredDomain
	}
	return strings.Split(subdomainPart, "."), registeredDomain
}

func insertWordEveryIndex(parts []string, rootDomain string, words []string, resultsChan chan<- string) {
	fullDomain := strings.Join(append(parts, rootDomain), ".")
	if strings.Count(fullDomain, ".") > maxLabels {
		return
	}
	for _, w := range words {
		// Insert the word at every position, including the front (i == 0).
		for i := 0; i <= len(parts); i++ {
			newParts := make([]string, 0, len(parts)+1)
			newParts = append(newParts, parts[:i]...)
			newParts = append(newParts, w)
			newParts = append(newParts, parts[i:]...)
			resultsChan <- strings.Join(newParts, ".") + "." + rootDomain
		}
	}
}

var numRegex = regexp.MustCompile(`\d{1,3}`)

func modifyNumbers(parts []string, rootDomain string, resultsChan chan<- string) {
	numCount := 3
	subdomainPart := strings.Join(parts, ".")
	if subdomainPart == "" {
		return
	}
	digits := numRegex.FindAllString(subdomainPart, -1)
	uniqueDigits := make(map[string]struct{})
	for _, d := range digits {
		if _, exists := uniqueDigits[d]; exists {
			continue
		}
		uniqueDigits[d] = struct{}{}
		val, err := strconv.Atoi(d)
		if err != nil {
			continue
		}
		for i := 1; i <= numCount; i++ {
			incStr := strconv.Itoa(val + i)
			resultsChan <- strings.Replace(subdomainPart, d, incStr, 1) + "." + rootDomain
			if val-i >= 0 {
				decStr := strconv.Itoa(val - i)
				resultsChan <- strings.Replace(subdomainPart, d, decStr, 1) + "." + rootDomain
			}
		}
	}
}

func environmentPrefix(parts []string, rootDomain string, resultsChan chan<- string) {
	environments := []string{"dev", "staging", "uat", "prod", "test", "qa"}
	for _, env := range environments {
		newParts := append([]string{env}, parts...)
		resultsChan <- strings.Join(newParts, ".") + "." + rootDomain
	}
}

func regionPrefixes(parts []string, rootDomain string, resultsChan chan<- string) {
	regions := []string{"us-east-1", "us-west-2", "eu-west-1", "eu-central-1", "ap-southeast-1"}
	for _, region := range regions {
		newParts := append([]string{region}, parts...)
		resultsChan <- strings.Join(newParts, ".") + "." + rootDomain
	}
}

// newResolver builds a single resolver that picks a fresh random upstream on
// every dial. Reusing one resolver per worker (instead of allocating one per
// query) avoids a large amount of per-lookup garbage while still spreading load
// across the resolver pool and giving each retry a different upstream.
func newResolver(resolvers []string, dialTimeout time.Duration) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			resolverAddr := resolvers[rand.Intn(len(resolvers))]
			d := net.Dialer{Timeout: dialTimeout}
			return d.DialContext(ctx, "udp", resolverAddr)
		},
	}
}

// resolves reports whether domain answers, retrying a few times so a single
// slow or flaky upstream doesn't produce a false negative. Each attempt dials a
// new random resolver (see newResolver).
func resolves(resolver *net.Resolver, domain string, attempts int, queryTimeout time.Duration) bool {
	for attempt := 0; attempt < attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		_, err := resolver.LookupHost(ctx, domain)
		cancel()
		if err == nil {
			return true
		}
		// Only a genuinely transient failure is worth another upstream; NXDOMAIN
		// and similar definitive answers are final.
		if dnsErr, ok := err.(*net.DNSError); ok && !dnsErr.IsTemporary && !dnsErr.IsTimeout {
			return false
		}
	}
	return false
}

func saveResumeFile(fileName string, remaining []string, workChan <-chan string) {
	log.Printf("Saving remaining work to %s...", fileName)
	seen := make(map[string]struct{})
	remainingWork := make([]string, 0, len(remaining))
	add := func(domain string) {
		if domain == "" {
			return
		}
		if _, dup := seen[domain]; dup {
			return
		}
		if _, attempted := attemptedDomains.Load(domain); attempted {
			return
		}
		seen[domain] = struct{}{}
		remainingWork = append(remainingWork, domain)
	}
	// Un-started seeds regenerate their full permutation set on resume.
	for _, d := range remaining {
		add(d)
	}
	// Plus any already-generated permutations still buffered in the pipeline.
	for domain := range workChan {
		add(domain)
	}
	if len(remainingWork) == 0 {
		log.Println("No remaining work to save.")
		return
	}
	file, err := os.Create(fileName)
	if err != nil {
		log.Printf("❌ Could not create resume file: %v", err)
		return
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, domain := range remainingWork {
		fmt.Fprintln(writer, domain)
	}
	writer.Flush()
	log.Printf("✅ Saved %d domains to resume file.", len(remainingWork))
}

func main() {
	fmt.Println(banner)
	fmt.Printf("%78s\n\n", colorCyan+"by CypherNova"+colorReset)

	subdomainsFile := flag.String("s", "", "Path to the subdomains file (optional, reads from stdin).")
	wordlistFile := flag.String("w", "words.txt", "Path to the wordlist file (falls back to the built-in list).")
	resolversFile := flag.String("r", "recommended_resolvers.txt", "Path to the DNS resolvers file (falls back to the built-in list).")
	outputFile := flag.String("o", "resolved_subdomains.txt", "Path to the output file.")
	threads := flag.Int("t", 100, "Number of concurrent DNS resolving threads.")
	rateLimit := flag.Int("l", 1000, "Max queries per second to send (0 = unlimited).")
	retries := flag.Int("retries", 2, "Resolution attempts per domain before giving up (min 1).")
	preValidate := flag.Bool("pre-validate", false, "Pre-validate that base domains are resolvable before generating permutations.")
	resumeFile := flag.String("resume", "", "Path to a resume file to continue a previous scan.")
	flag.Parse()

	// Track which flags the user explicitly set so a missing default file can
	// silently fall back to the embedded list, while an explicit -w/-r path that
	// is missing is treated as a real error the user asked for.
	flagSet := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { flagSet[f.Name] = true })

	// --- Input validation: bad flags should be a clean error, never a panic. ---
	if *threads < 1 {
		log.Fatalf("❌ -t (threads) must be at least 1, got %d", *threads)
	}
	if *rateLimit < 0 {
		log.Fatalf("❌ -l (rate limit) cannot be negative, got %d", *rateLimit)
	}
	if *retries < 1 {
		*retries = 1
	}

	log.Println("🚀 Starting DNS-Helix...")

	var baseSubdomains []string
	var err error
	isResuming := *resumeFile != ""
	if isResuming {
		log.Printf("Resuming scan from %s", *resumeFile)
		baseSubdomains, err = readLines(*resumeFile)
		if err != nil {
			log.Fatalf("❌ Could not read resume file: %v", err)
		}
	} else {
		if *subdomainsFile != "" {
			baseSubdomains, err = readLines(*subdomainsFile)
			if err != nil {
				log.Fatalf("❌ Could not read subdomains file: %v", err)
			}
		} else {
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				log.Println("📖 Reading base subdomains from stdin...")
				scanner := bufio.NewScanner(os.Stdin)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line != "" {
						baseSubdomains = append(baseSubdomains, line)
					}
				}
				if err := scanner.Err(); err != nil {
					log.Fatalf("❌ Could not read from stdin: %v", err)
				}
			} else {
				log.Fatalf("❌ No subdomains provided. Use -s or -resume, or pipe input.")
			}
		}
	}
	initialCount := len(baseSubdomains)
	if initialCount == 0 {
		log.Fatalf("❌ No base domains to process.")
	}
	log.Printf("Loaded %d domains to process.", initialCount)

	// The wordlist drives permutation generation, which runs for both fresh and
	// resumed scans, so it is always loaded. If the on-disk file is missing we
	// fall back to the built-in list (unless the user explicitly pointed -w at a
	// path); a genuinely empty wordlist is non-fatal because the number and
	// prefix mutations still generate useful candidates.
	words, err := readLines(*wordlistFile)
	if err != nil {
		if !flagSet["w"] {
			words = parseLines(embeddedWords)
			log.Printf("Loaded %d words from built-in wordlist.", len(words))
		} else {
			log.Printf("⚠️  Could not read wordlist file (%v); continuing with number/prefix mutations only.", err)
		}
	} else {
		log.Printf("Loaded %d words from wordlist.", len(words))
	}

	resolvers, err := readLines(*resolversFile)
	if err != nil {
		if !flagSet["r"] {
			resolvers = parseLines(embeddedResolvers)
			log.Printf("Loaded %d DNS resolvers from built-in list.", len(resolvers))
		} else {
			log.Fatalf("❌ Could not read resolvers file: %v", err)
		}
	}
	for i, r := range resolvers {
		if !strings.Contains(r, ":") {
			resolvers[i] = r + ":53"
		}
	}
	if len(resolvers) == 0 {
		log.Fatalf("❌ No DNS resolvers loaded from %s.", *resolversFile)
	}
	log.Printf("Loaded %d DNS resolvers.", len(resolvers))

	const dialTimeout = 1 * time.Second
	const queryTimeout = 2 * time.Second

	if *preValidate && !isResuming {
		log.Println("🔍 Pre-validation enabled. Checking base domains first...")

		validationJobs := make(chan string, *threads)
		validatedChan := make(chan string, *threads)
		var validationWg sync.WaitGroup

		var collectorWg sync.WaitGroup
		var validatedSubdomains []string
		collectorWg.Add(1)
		go func() {
			defer collectorWg.Done()
			for domain := range validatedChan {
				validatedSubdomains = append(validatedSubdomains, domain)
			}
		}()

		for i := 0; i < *threads; i++ {
			validationWg.Add(1)
			go func() {
				defer validationWg.Done()
				resolver := newResolver(resolvers, dialTimeout)
				for domain := range validationJobs {
					if resolves(resolver, domain, *retries, queryTimeout) {
						validatedChan <- domain
					}
				}
			}()
		}

		for _, domain := range baseSubdomains {
			validationJobs <- domain
		}
		close(validationJobs)

		validationWg.Wait()
		close(validatedChan)
		collectorWg.Wait()

		baseSubdomains = validatedSubdomains
		log.Printf("✅ Pre-validation complete. %d of %d base domains are valid.", len(baseSubdomains), initialCount)
		if len(baseSubdomains) == 0 {
			log.Println("No valid base domains to process. Exiting.")
			return
		}
	}

	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	defer cancelShutdown()

	workChan := make(chan string, *threads*4)
	resolvedChan := make(chan string, *threads*4)
	var generatorWg, resolverWg, collectorWg sync.WaitGroup
	var generatedCount, attemptedCount, resolvedCount atomic.Int64
	// seedCursor is the index of the next un-started seed; on interrupt,
	// baseSubdomains[seedCursor:] is everything whose permutations were never
	// (fully) generated and must be preserved for --resume.
	var seedCursor atomic.Int64

	// Optional global rate limiter. A zero/omitted rate means unlimited.
	var rateChan <-chan time.Time
	var rateLimiter *time.Ticker
	if *rateLimit > 0 {
		interval := time.Second / time.Duration(*rateLimit)
		if interval <= 0 {
			interval = time.Nanosecond
		}
		rateLimiter = time.NewTicker(interval)
		rateChan = rateLimiter.C
	}

	collectorWg.Add(1)
	foundSubdomains := make(map[string]struct{})
	go func() {
		defer collectorWg.Done()
		for domain := range resolvedChan {
			if _, exists := foundSubdomains[domain]; !exists {
				foundSubdomains[domain] = struct{}{}
				resolvedCount.Add(1)
			}
		}
	}()

	resolverWg.Add(*threads)
	for i := 0; i < *threads; i++ {
		go func() {
			defer resolverWg.Done()
			resolver := newResolver(resolvers, dialTimeout)
			for {
				select {
				case domain, ok := <-workChan:
					if !ok {
						return
					}
					// Skip anything already attempted so overlapping mutations
					// never cost a second network round-trip.
					if _, loaded := attemptedDomains.LoadOrStore(domain, true); loaded {
						continue
					}
					attemptedCount.Add(1)
					if rateChan != nil {
						select {
						case <-rateChan:
						case <-shutdownCtx.Done():
							return
						}
					}
					if resolves(resolver, domain, *retries, queryTimeout) {
						select {
						case resolvedChan <- domain:
						case <-shutdownCtx.Done():
							return
						}
					}
				case <-shutdownCtx.Done():
					return
				}
			}
		}()
	}

	generatorWg.Add(1)
	go func() {
		defer generatorWg.Done()
		for idx, domain := range baseSubdomains {
			seedCursor.Store(int64(idx))
			select {
			case workChan <- domain:
				generatedCount.Add(1)
			case <-shutdownCtx.Done():
				return
			}

			parts, rootDomain := partiateDomain(domain)
			if rootDomain == "" {
				continue
			}

			var permWg sync.WaitGroup
			permChan := make(chan string, 2000)
			permWg.Add(1)
			go func() {
				defer permWg.Done()
				for p := range permChan {
					select {
					case workChan <- p:
						generatedCount.Add(1)
					case <-shutdownCtx.Done():
						// Keep draining permChan so the producers below don't
						// block; the work is dropped because we're shutting down.
					}
				}
			}()
			insertWordEveryIndex(parts, rootDomain, words, permChan)
			modifyNumbers(parts, rootDomain, permChan)
			environmentPrefix(parts, rootDomain, permChan)
			regionPrefixes(parts, rootDomain, permChan)
			close(permChan)
			permWg.Wait()

			if shutdownCtx.Err() != nil {
				return
			}
		}
		// All seeds fully dispatched.
		seedCursor.Store(int64(len(baseSubdomains)))
	}()

	log.Println("🔥 Processing domains...")
	startTime := time.Now()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	go func() {
		generatorWg.Wait()
		close(workChan)
	}()
	done := make(chan struct{})
	go func() {
		resolverWg.Wait()
		close(done)
	}()

	interrupted := false
	running := true
	for running {
		select {
		case <-done:
			running = false
			fmt.Println()
		case <-sigs:
			interrupted = true
			running = false
			cancelShutdown()
			fmt.Println()
			log.Println("⚠️  Ctrl+C detected. Shutting down...")
		case <-ticker.C:
			elapsed := time.Since(startTime).Seconds()
			var qps float64
			if elapsed > 0 {
				qps = float64(attemptedCount.Load()) / elapsed
			}
			fmt.Printf("\r💡 Generated: %-8d | Attempted: %-8d | Resolved: %-5d | %.0f q/s   ",
				generatedCount.Load(), attemptedCount.Load(), resolvedCount.Load(), qps)
		}
	}

	resolverWg.Wait()

	close(resolvedChan)
	collectorWg.Wait()
	if rateLimiter != nil {
		rateLimiter.Stop()
	}

	if interrupted {
		// Everything from the cursor onward is a seed whose permutations were
		// never (fully) generated, so it must be preserved for --resume.
		var remaining []string
		if cursor := int(seedCursor.Load()); cursor < len(baseSubdomains) {
			remaining = baseSubdomains[cursor:]
		}
		resumeFileName := fmt.Sprintf("resume-%s.log", time.Now().Format("20060102-150405"))
		saveResumeFile(resumeFileName, remaining, workChan)
	}

	finalMap := make(map[string]struct{})
	if _, err := os.Stat(*outputFile); err == nil {
		existingDomains, err := readLines(*outputFile)
		if err == nil {
			log.Printf("📖 Found existing output file. Merging %d domains.", len(existingDomains))
			for _, d := range existingDomains {
				finalMap[d] = struct{}{}
			}
		}
	}
	if !isResuming {
		for _, d := range baseSubdomains {
			finalMap[d] = struct{}{}
		}
	}
	for d := range foundSubdomains {
		finalMap[d] = struct{}{}
	}
	var finalDomains []string
	for d := range finalMap {
		finalDomains = append(finalDomains, d)
	}
	sort.Strings(finalDomains)

	log.Printf("✨ Found a total of %d unique subdomains.", len(finalDomains))
	file, err := os.Create(*outputFile)
	if err != nil {
		log.Fatalf("❌ Could not create output file: %v", err)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, d := range finalDomains {
		fmt.Fprintln(writer, d)
	}
	writer.Flush()
	log.Printf("🎉 Success! All unique results saved to %s", *outputFile)
}
