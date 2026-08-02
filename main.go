package main

import (
	"bufio"
	"context"
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

var (
	attemptedDomains sync.Map
)

const (
	colorCyan  = "\033[96m"
	colorReset = "\033[0m"
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

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
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
	if strings.Count(fullDomain, ".") > 6 {
		return
	}
	for _, w := range words {
		newParts := append([]string{w}, parts...)
		resultsChan <- strings.Join(newParts, ".") + "." + rootDomain
		for i := 1; i <= len(parts); i++ {
			tempParts := make([]string, len(parts))
			copy(tempParts, parts)
			withWord := append(tempParts[:i], append([]string{w}, tempParts[i:]...)...)
			resultsChan <- strings.Join(withWord, ".") + "." + rootDomain
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

func saveResumeFile(fileName string, workChan <-chan string) {
	log.Printf("Saving remaining work to %s...", fileName)
	remainingWork := []string{}
	for domain := range workChan {
		if _, attempted := attemptedDomains.Load(domain); !attempted {
			remainingWork = append(remainingWork, domain)
		}
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
	wordlistFile := flag.String("w", "words.txt", "Path to the wordlist file.")
	resolversFile := flag.String("r", "resolvers.txt", "Path to the DNS resolvers file.")
	outputFile := flag.String("o", "resolved_subdomains.txt", "Path to the output file.")
	threads := flag.Int("t", 100, "Number of concurrent DNS resolving threads.")
	rateLimit := flag.Int("l", 1000, "Max queries per second to send.")
	preValidate := flag.Bool("pre-validate", false, "Pre-validate that base domains are resolvable before generating permutations.")
	resumeFile := flag.String("resume", "", "Path to a resume file to continue a previous scan.")
	flag.Parse()

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
	log.Printf("Loaded %d domains to process.", initialCount)

	var words []string
	if !isResuming {
		words, err = readLines(*wordlistFile)
		if err != nil {
			log.Fatalf("❌ Could not read wordlist file: %v", err)
		}
		log.Printf("Loaded %d words from wordlist.", len(words))
	}

	resolvers, err := readLines(*resolversFile)
	if err != nil {
		log.Fatalf("❌ Could not read resolvers file: %v", err)
	}
	for i, r := range resolvers {
		if !strings.Contains(r, ":") {
			resolvers[i] = r + ":53"
		}
	}
	log.Printf("Loaded %d DNS resolvers.", len(resolvers))

	if *preValidate && !isResuming {
		log.Println("🔍 Pre-validation enabled. Checking base domains first...")

		validationJobs := make(chan string, initialCount)
		validatedChan := make(chan string, initialCount)
		var validationWg sync.WaitGroup

		// Start a collector goroutine to drain the validatedChan concurrently
		var collectorWg sync.WaitGroup
		var validatedSubdomains []string
		collectorWg.Add(1)
		go func() {
			defer collectorWg.Done()
			for domain := range validatedChan {
				validatedSubdomains = append(validatedSubdomains, domain)
			}
		}()

		// Start worker pool
		for i := 0; i < *threads; i++ {
			validationWg.Add(1)
			go func() {
				defer validationWg.Done()
				for domain := range validationJobs {
					resolverAddr := resolvers[rand.Intn(len(resolvers))]
					resolver := &net.Resolver{PreferGo: true,
						Dial: func(ctx context.Context, n, a string) (net.Conn, error) {
							return net.DialTimeout(n, resolverAddr, 1*time.Second)
						},
					}
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_, err := resolver.LookupHost(ctx, domain)
					if err == nil {
						validatedChan <- domain
					}
					cancel()
				}
			}()
		}

		// Feed the jobs and close the channel
		for _, domain := range baseSubdomains {
			validationJobs <- domain
		}
		close(validationJobs)

		// Wait for workers to finish, then close validatedChan and wait for collector
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
	rateLimiter := time.NewTicker(time.Second / time.Duration(*rateLimit))

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
			for {
				select {
				case domain, ok := <-workChan:
					if !ok {
						return
					}
					attemptedCount.Add(1)
					attemptedDomains.Store(domain, true)
					select {
					case <-rateLimiter.C:
					case <-shutdownCtx.Done():
						return
					}
					resolverAddr := resolvers[rand.Intn(len(resolvers))]
					resolver := &net.Resolver{
						PreferGo: true,
						Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
							d := net.Dialer{Timeout: time.Second * 1}
							return d.DialContext(ctx, "udp", resolverAddr)
						},
					}
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_, err := resolver.LookupHost(ctx, domain)
					cancel()
					if err == nil {
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
		if isResuming {
			for _, domain := range baseSubdomains {
				select {
				case workChan <- domain:
					generatedCount.Add(1)
				case <-shutdownCtx.Done():
					return
				}
			}
		} else {
			for _, domain := range baseSubdomains {
				select {
				case workChan <- domain:
					generatedCount.Add(1)
				case <-shutdownCtx.Done():
					return
				}
				if shutdownCtx.Err() != nil {
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
						}
					}
				}()
				insertWordEveryIndex(parts, rootDomain, words, permChan)
				modifyNumbers(parts, rootDomain, permChan)
				environmentPrefix(parts, rootDomain, permChan)
				regionPrefixes(parts, rootDomain, permChan)
				close(permChan)
				permWg.Wait()
			}
		}
	}()

	log.Println("🔥 Processing domains...")
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
			fmt.Printf("\r💡 Generated: %-8d | Attempted: %-8d | Resolved: %-5d",
				generatedCount.Load(), attemptedCount.Load(), resolvedCount.Load())
		}
	}

	resolverWg.Wait()

	close(resolvedChan)
	collectorWg.Wait()
	rateLimiter.Stop()

	if interrupted {
		resumeFileName := fmt.Sprintf("resume-%s.log", time.Now().Format("20060102-150405"))
		saveResumeFile(resumeFileName, workChan)
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
