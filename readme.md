# 💰 $alary $leuth 💰
This is a Go program that checks the median salaries of software engineers for companies that have a job posting with a certain keyword in their job description. It searches LinkedIn for job postings and the levels.fyi website to retrieve salary information for a given company.

## Usage
```bash
salarysleuth [-d | --description job_characteristic] [-l | --location location] [-t | --title title_keyword] [-r | --remote] [--internships] [-s | --silence] [-h | --help]
```

## Options
* `-d job_characteristic`, `--description job_characteristic` - Job characteristic or keyword to search for in the job description on LinkedIn
* `-l location`, `--location location` - City name to search for jobs on LinkedIn, or 'United States' for nationwide search (default: "United States")
* `-t title_keyword`, `--title title_keyword` - Optional: Keyword to search for in job titles on LinkedIn
* `-r`, `--remote` - Optional: Retrieve only remote jobs or jobs listed under "United States"
* `--internships` - Optional: Retrieve only jobs with "intern" in the title
* `-s`, `--silence` - Optional: Silence the banner
* `-h`, `--help` - Optional. Displays the help menu.

## Examples
- Search for remote jobs across the United States that mention "OSCP" in the job description:
```bash
salarysleuth -d "OSCP" -r
```

- Search only for internships that include "Software Engineer" in the title:
```bash
salarysleuth -d "Software Engineer" --internships
```

- Search for jobs in "San Francisco, CA" that mention "Metasploit" in the job title, and silence the banner:
```bash
salarysleuth -d "Metasploit" -l "San Francisco, CA" -s
```

### Docker
```bash
docker build -t salarysleuth .
docker run -it salarysleuth salarysleuth --help
```

![Alt Text](https://github.com/fr4nk3nst1ner/salarysleuth/blob/main/resources/salarysleuth_3.gif)

## Requirements
* Go

## Installation
```bash
git clone https://github.com/fr4nk3nst1ner/salarysleuth.git
cd salarysleuth
go build -o salarysleuth
./salarysleuth -h
```

## To Do

- [x] Return job titles in search results
- [x] Create flag to return only remote jobs
- [x] Add `--internships` flag to return only internships
- [ ] Add jobs.smartrecruiters.com and builtin.com as sources for more job returns
- [ ] Optimize speed and make searches take less time, particularly for higher page searches
- [ ] Finish search engine implementation
- [ ] Fix some misc error handling bugs
- [ ] Extend features to other pre-auth job search engines
- [ ] Add in capability of retrieving median salary for non-SWE (i.e., software engineering manager)

## Disclaimer
This program is for educational and informational purposes only. The salary information provided is not guaranteed to be accurate or up-to-date.

