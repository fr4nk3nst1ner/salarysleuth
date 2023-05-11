# 💰 $alary $leuth 💰
This is a Python program that checks the median salaries of software engineers for companies that have a job posting with a certain keyword in their job description. It uses the [go-dork](https://github.com/dwisiswant0/go-dork) command line tool (shoutout to [dwisiswant0](https://github.com/dwisiswant0)) to search for job postings, and the levels.fyi website to retrieve salary information for a given company.

## Usage
```bash
python salarysleuth.py [-j | --job job_characteristic] [-c | --company companyname] [-p | --pages pages] [-e | --engine engine] [-t --table] [-h | --help]
```

## Options
* `-j job_characteristic`, `--job job_characteristic` - Job characteristic to search for on job listing websites
* `-c company`, `--company company` - Name of a specific company to search for salary information
* `-p pages`, `--pages pages` - Optional: Number of pages you'd like to dork (default: 50)
* `-e engine`, `--engine engine` - Optional: The search engine you'd like to use (default: google) Options: Google, Shodan, Bing, Duck, Yahoo, Ask 
* `-t`, `--table` - Optional: Re-organize output into a table in ascending order based on median salary (default: false)
* `-r`, `--remote` - Optional: Retrieve only remote jobs
* `-h`, `--help` - Optional. Displays the help menu.

Note: Only tested with Google

## Examples
- Search 20 Google pages for jobs that contain "Penetration Test" in its job description but only return data where median salary exist on levels[.]fyi:
```bash
python salarysleuth.py -j "Penetration Test" -t -p20
```

- Search 40 Google pages for jobs that contain "OSCP", returning data regardless of if median salary exists on levels[.]fyi:
```bash
python salarysleuth.py -j "OSCP" -p 40 
```

- Perform a single lookup of a company to determine median software engineer salary on leves[.]fyi:
```bash
python salarysleuth.py -c "Salesforce"
```

### Docker
```bash
docker build -t salarysleuth .
docker run -it salarysleuth salarysleuth.py --help
```
![Alt Text](https://github.com/fr4nk3nst1ner/salarysleuth/blob/main/resources/salarysleuth_3.gif)

## Requirements
* Python 3.6 or higher
* Go
* go-dork command line tool
* tqdm
* BeautifulSoup and requests Python packages (`pip install -r requirements.txt`)

## Installation
```bash
git clone https://github.com/fr4nk3nst1ner/salarysleuth.git
cd salarysleuth
pip install -r requirements.txt
GO111MODULE=on go install dw1.io/go-dork@latest
python salarysleuth.py -h
```

## To Do

- [x] Return job titles in search results
- [x] Create flag to return only remote jobs 
- [ ] Add jobs.smartrecruiters.com as a source for more job returns
- [ ] Optimize speed and make searches take less time, particularly for higher page searches
- [ ] Finish search engine implementation
- [ ] Fix some misc error handling bugs 
- [ ] Extend features to other pre-auth job search engines 
- [ ] Add in capability of retrieving median salary for non-SWE (i.e., software engineering manager)

## Disclaimer
This program is for educational and informational purposes only. The salary information provided is not guaranteed to be accurate or up-to-date.

## Shoutout
Tip of the hat to [dwisiswant0](https://github.com/dwisiswant0) for [go-dork](https://github.com/dwisiswant0/go-dork)

