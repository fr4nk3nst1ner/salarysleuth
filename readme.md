# 💰 Salary Checker 💰
This is a Python program that checks the median salaries of software engineers for companies that have a job posting with a certain keyword in their job description. It uses the go-dork command line tool to search for job postings, and the levels.fyi website to retrieve salary information for a given company.

## Usage
```bash
python salarychecker.py [-j | --job job_characteristic] [-c | --company companyname] [-h | --help]
```

## Options
* `-j job_characteristic`, `--job job_characteristic` - Job characteristic to search for on job listing websites
* `-c company`, `--company company` - Name of a specific company to search for salary information
* `-h`, `--help` - Optional. Displays the help menu.

## Example
```bash
python salarychecker.py -j "Penetration Test"
python salarychecker.py -j "OSCP"

python salarychecker.py -c "Salesforce"
python salarychecker.py -c "Rapid7"
```

## Installation
1. Clone the repository: git clone https://github.com/fr4nk3nst1ner/salarychecker.git
2. Install the required Python packages
3. Install [go-dork](https://github.com/dwisiswant0/go-dork)
4. Run the program: python salarychecker.py [job_title]

## Requirements
* Python 3.6 or higher
* go-dork command line tool
* BeautifulSoup and requests Python packages

## Disclaimer
This program is for educational and informational purposes only. The salary information provided is not guaranteed to be accurate or up-to-date.
