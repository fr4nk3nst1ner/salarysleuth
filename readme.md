# 💰 Salary Checker 💰
This is a Python program that checks the median salaries of software engineers for companies that have a job posting with a certain keyword in their job description. It uses the go-dork command line tool to search for job postings, and the levels.fyi website to retrieve salary information for a given company.

## Usage
```bash
python salarysleuth.py [-j | --job job_characteristic] [-c | --company companyname] [-p | --pages pages] [-e | --engine engine] [-h | --help]
```

## Options
* `-j job_characteristic`, `--job job_characteristic` - Job characteristic to search for on job listing websites
* `-c company`, `--company company` - Name of a specific company to search for salary information
* `-p pages`, `--pages pages` - Number of pages you'd like to dork (default: 50)
* `-e engine`, `--engine engine` - The search engine you'd like to use (default: google) Options: Google, Shodan, Bing, Duck, Yahoo, Ask 
* `-h`, `--help` - Optional. Displays the help menu.

Note: Only tested with Google

## Example
```bash
python salarysleuth.py -j "Penetration Test"
python salarysleuth.py -j "OSCP"

python salarysleuth.py -c "Salesforce"
python salarysleuth.py -c "Rapid7"
```

## Requirements
* Python 3.6 or higher
* Go
* go-dork command line tool
* BeautifulSoup and requests Python packages (`pip install -r requirements.txt`)

## Installation
1. git clone https://github.com/fr4nk3nst1ner/salarysleuth.git
2. cd salarysleuth
3. pip install -r requirements.txt
3. GO111MODULE=on go install dw1.io/go-dork@latest
4. python salarysleuth.py -h

## Disclaimer
This program is for educational and informational purposes only. The salary information provided is not guaranteed to be accurate or up-to-date.

## Shoutout
Tip of the hat to [dwisiswant0](https://github.com/dwisiswant0) for [go-dork](https://github.com/dwisiswant0/go-dork).
