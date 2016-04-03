package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// Define Binary pattern here:
var binary_pattern = regexp.MustCompile(`\.(xls|doc|o|a|zip|rar)$`)

var infile *string = flag.String("f", "", "File contains commit id, one commit one each line")
var gitdir *string = flag.String("d", "", "Repo path")
var job *int = flag.Int("j", 4, "Number of jobs")
var wg sync.WaitGroup
var run_queue chan string
var job_queue chan int
var result chan string
var stderr chan string

func do_commit_check(commit string) {
	var parents [2]string

	out, err := exec.Command("git", "cat-file", "-p", commit).Output()
	if err != nil {
		log.Fatal(err)
	}
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "parent ") {
			parents[count] = strings.Split(line, " ")[1]
			count++
			if count > 2 {
				stderr <- fmt.Sprintf("Error: %s is a octopus commit\n", commit)
				return
			}
		}
	}
	if count < 2 {
		stderr <- fmt.Sprintf("Error: %s is not a merge commit\n", commit)
		return
	}

	out, err = exec.Command("git", "merge-base", parents[0], parents[1]).Output()
	if err != nil {
		stderr <- fmt.Sprintf("Error: Can not find merge-base of %s\n", commit)
		return
	}
	merge_base := strings.TrimSpace(string(out))

	var diff_base_mine, diff_base_theirs, diff_merge_mine, diff_merge_theirs int
	_, diff_base_mine = get_diff_tree(merge_base, parents[0])
	_, diff_base_theirs = get_diff_tree(merge_base, parents[1])
	_, diff_merge_mine = get_diff_tree(commit, parents[0])
	_, diff_merge_theirs = get_diff_tree(commit, parents[1])

	bias1 := eval_bias(diff_merge_theirs, diff_base_mine)
	bias2 := eval_bias(diff_merge_mine, diff_base_theirs)
	var bias int
	if bias1 > bias2 {
		bias = bias1
	} else {
		bias = bias2
	}

	result <- fmt.Sprintf("%-40s: %d\n", commit, bias)
	<-job_queue
}

func get_diff_tree(commit1, commit2 string) (string, int) {
	out, err := exec.Command("git", "diff-tree", "-r", commit1, commit2).Output()
	if err != nil {
		log.Fatal(err)
	}
	diff := strings.TrimSpace(string(out))
	count := 0
	for _, item := range strings.Split(diff, "\n") {
		if binary_pattern.MatchString(item) {
			continue
		}
		count++
	}
	return diff, count
}

func eval_bias(real_value, expect int) int {
	bias := 0
	if expect == 0 {
		if real_value == expect {
			bias = 0
		} else {
			bias = 100
		}
	} else if real_value == 0 {
		bias = 100
	} else {
		bias = int(int(math.Abs(float64(real_value)-float64(expect))) * 100 / expect)
		if bias > 100 {
			bias = 100
		}
	}
	return bias
}

func main() {
	commit_list := make([]string, 0, 10)

	flag.Parse()

	if *infile != "" {
		file, err := os.Open(*infile)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			commit := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(commit, "#") {
				continue
			}
			commit_list = append(commit_list, commit)
		}

		if err := scanner.Err(); err != nil {
			log.Fatal(err)
		}
	} else if len(flag.Args()) != 0 {
		for _, commit := range flag.Args() {
			commit_list = append(commit_list, commit)
		}
	}

	if *gitdir != "" {
		err := os.Chdir(*gitdir)
		if err != nil {
			log.Fatal(err)
		}
	}

	if *job < 1 {
		*job = 1
	}

	job_queue = make(chan int, *job)
	run_queue = make(chan string)
	result = make(chan string)
	stderr = make(chan string)

	wg.Add(len(commit_list))

	go func() {
		for out := range stderr {
			fmt.Fprintf(os.Stderr, out)
		}
	}()

	go func() {
		for out := range result {
			fmt.Fprintf(os.Stdout, out)
		}
	}()

	go func() {
		for {
			c, ok := <-run_queue
			if !ok {
				break
			}
			job_queue <- 1
			go func(commit string) {
				do_commit_check(commit)
				wg.Done()
			}(c)
		}
		close(job_queue)
	}()

	fmt.Fprintf(os.Stderr, "%-40s: %s\n", "COMMIT", "BIAS(less is good)")
	fmt.Fprintf(os.Stderr, "--------------------------------------------------\n")
	for _, commit := range commit_list {
		run_queue <- commit
	}

	close(run_queue)
	wg.Wait()
	close(result)
	close(stderr)
}
