# 💰 $alary $leuth 💰
Program that checks the median salaries of software engineers for companies that have a job posting with a certain keyword in their job description. It searches job boards like LinkedIn for job postings and uses levels.fyi to retrieve salary information for companies.

## Usage
```bash
salarysleuth [-description job_characteristic] [-city location] [-title title_keyword] [-pages num_pages] 
             [-source source_name] [-remote] [-internships] [-top-pay] [-top-paying-companies]
             [-table] [-no-levels] [-proxy proxy_url] [-debug] [-examples] [-silence | -nobanner] [-help]
```

## Options
* `-description job_characteristic` - Job characteristic or keyword to search for in the job description
* `-city location` - City name to search for jobs, or 'United States' for nationwide search
* `-title title_keyword` - Optional: Keyword to search for in job titles
* `-pages num_pages` - Number of pages to scrape (default: 1)
* `-source source_name` - Source to scrape (linkedin, greenhouse, lever, monster, indeed). If not specified, searches LinkedIn.
* `-remote` - Only show remote positions
* `-internships` - Only show jobs with "intern" or "internship" in the title
* `-top-pay` - Only show jobs from companies listed in levels.fyi's top paying companies list
* `-top-paying-companies` - Show the list of top paying companies from levels.fyi
* `-table` - Show results in table format (only jobs with Levels.fyi data)
* `-no-levels` - Skip fetching salary data from Levels.fyi
* `-proxy proxy_url` - Proxy URL to use for requests
* `-debug` - Enable debug mode with verbose output
* `-examples` - Display usage examples for the tool
* `-silence` or `-nobanner` - Silence the banner
* `-help` - Displays the help menu

## Examples
- Search for remote jobs across the United States that mention "OSCP" in the job description:
```bash
salarysleuth -description "OSCP" -remote
```

- Search only for internships that include "Software Engineer" in the title:
```bash
salarysleuth -description "Software Engineer" -internships
```

- Search for jobs in "San Francisco, CA" that mention "Metasploit" in the job title, and silence the banner:
```bash
salarysleuth -description "Metasploit" -city "San Francisco, CA" -silence
```

- Search for jobs at top paying tech companies that mention "Python":
```bash
salarysleuth -description "Python" -top-pay
```

- Display the list of top paying companies according to levels.fyi:
```bash
salarysleuth -top-paying-companies
```

- Search for "Software Engineer" jobs across multiple pages with a proxy:
```bash
salarysleuth -description "Software Engineer" -pages 3 -proxy http://localhost:8080
```

- Search for jobs on Indeed and display results in table format:
```bash
salarysleuth -description "DevOps" -source indeed -table
```

- Display usage examples for the tool:
```bash
salarysleuth -examples
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
./salarysleuth -help
```

## To Do

- [x] Return job titles in search results
- [x] Create flag to return only remote jobs
- [x] Add `-internships` flag to return only internships
- [x] Add `-top-paying-companies` flag to show top paying companies from levels.fyi
- [x] Add `-table` flag to display results in table format
- [ ] Support multiple job sources (LinkedIn, Greenhouse, Lever, Monster, Indeed)
- [x] Add `-examples` flag to display usage examples
- [ ] Add jobs.smartrecruiters.com and builtin.com as sources for more job returns
- [ ] Optimize speed and make searches take less time, particularly for higher page searches
- [ ] Finish search engine implementation
- [ ] Fix some misc error handling bugs
- [ ] Extend features to other pre-auth job search engines
- [ ] Add in capability of retrieving median salary for non-SWE (i.e., software engineering manager)

## Disclaimer
This program is for educational and informational purposes only. The salary information provided is not guaranteed to be accurate or up-to-date.

