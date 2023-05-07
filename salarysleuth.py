#!/usr/local/bin/python3
import argparse
from argparse import RawTextHelpFormatter, SUPPRESS
import re
import subprocess
import requests
from bs4 import BeautifulSoup
from tqdm import tqdm


BANNER = """
\033[38;5;196m$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
$$$                                                      $$$
$$$                     $alary $leuth                    $$$
$$$                     @fr4nk3nst1ner                   $$$
$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
\033[0m
"""


def get_job_urls(query, num_pages, search_engine, remote_only=False):
    # Define the go-dork command
    godork_cmd = f'go-dork -e {search_engine} -p {num_pages} -s -q "{query}"'

    # Run the go-dork command and capture the output
    output = subprocess.check_output(godork_cmd, shell=True, stderr=subprocess.DEVNULL)

    # Decode the output as a string
    output_str = output.decode('utf-8')

    # Extract the URLs from the output using regular expressions
    job_urls = re.findall(r'https?://\S+', output_str)

    # Extract the job titles from the URLs using BeautifulSoup
    job_data = []
    for url in job_urls:
        response = requests.get(url)
        soup = BeautifulSoup(response.content, 'html.parser')
        if 'lever.co' in url:
            commitment_elem = soup.find('div', {'class': 'sort-by-commitment posting-category medium-category-label commitment'})
            location_elem = soup.find('div', {'class': 'location'})
            if remote_only and (not commitment_elem or 'remote' not in commitment_elem.text.lower()):
                continue
            title_elem = soup.find('h2')
            if title_elem:
                title = title_elem.text.strip()
            else:
                title = ''
        elif 'greenhouse.io' in url:
            location_elem = soup.find('div', {'class': 'location'})
            if remote_only and (not location_elem or 'remote' not in location_elem.text.lower()):
                continue
            title_elem = soup.find('h1')
            if title_elem:
                title = title_elem.text.strip()
            else:
                title = ''
        else:
            title = ''
        job_data.append({'title': title, 'url': url})

    return job_data





def get_salary(company_name, use_decimal=False):
    url = f"https://www.levels.fyi/companies/{company_name}/salaries/"

    # Make request to levels page and raise an exception if there's an error
    response = requests.get(url)
    html = response.content.decode()

    # Extract salary info using BeautifulSoup
    soup = BeautifulSoup(html, "html.parser")
    salary_elem = soup.find("td", string="Software Engineer Salary")
    if salary_elem:
        salary = salary_elem.find_next_sibling("td").text
        salary = salary.replace(",", "").replace("$", "")
        salary = float(salary) if use_decimal else int(salary)
        return {'company': company_name, 'salary': salary}

    return None

def get_company_name(url_dict):
    # Extract the company name from the URL dictionary
    url = url_dict['url']
    match = re.search(r'\/\/[^/]+\/([^/]+)\/', url)
    if match:
        company_name = match.group(1)
    else:
        company_name = ''

    return company_name


def print_banner(silence):
    if not silence:
        print(BANNER)

def colorize_salary(salary):
    if salary is None:
        return 'No Data'
    salary = str(salary).replace('$', '').replace(',', '')
    salary = re.sub('\x1b\[.*?m', '', salary)  # Remove color codes
    salary = float(salary)
    
    if salary >= 300000:
        return "\033[32m${:,.0f}\033[0m".format(salary)
    elif salary >= 200000:
        return "\033[92m${:,.0f}\033[0m".format(salary)
    elif salary >= 100000:
        return "\033[93m${:,.0f}\033[0m".format(salary)
    else:
        return "\033[31m${:,.0f}\033[0m".format(salary)


def main():
    parser = argparse.ArgumentParser(description="Examples: \n python salarysleuth.py -j kali \n python salarysleuth.py -j oscp \n python salarysleuth.py -c rapid7 \n python salarysleuth.py -c salesforce", formatter_class=RawTextHelpFormatter,usage=SUPPRESS)
    parser.add_argument("-j", "--job", type=str, help="Job characteristic to search for on job listing websites")
    parser.add_argument("-c", "--company", type=str, help="Name of a specific company to search for salary information")
    parser.add_argument("-s", "--silence", action="store_true", help="Silence the banner")
    parser.add_argument("-p", "--pages", type=int, default=50, help="Number of search result pages to scrape (default: 50)")
    parser.add_argument("-e", "--engine", type=str, default='google', help="Search engine to use (default: google). \n Options: Google, Shodan, Bing, Duck, Yahoo, Ask \n Note: Only tested with Google")
    parser.add_argument("-t", "--table", action="store_true", help="Re-organize output into a table in ascending order based on median salary")
    parser.add_argument("-r", "--remote", action="store_true", help="Retrieve only remote jobs")

    args = parser.parse_args()

    if not args.job and not args.company:
        print("Please provide a job title or company name to search for. Use --help for usage details.")
        return

    print_banner(args.silence)

    # do stuff if `-j` is passed, regardless if `-t` is passed 
    if args.job:
        dork_query = f"site:lever.co OR site:greenhouse.io {args.job}"
        job_urls = get_job_urls(dork_query, args.pages, args.engine, remote_only=args.remote)


        # prep salaries / url dict
        salaries = []
        for url_dict in tqdm(job_urls, desc='Processing job URLs'):
            url = url_dict['url']
            job_title = url_dict['title']
            company_name = get_company_name(url_dict)
            salary_dict = get_salary(company_name)
            if salary_dict is not None:
                # Add URL, job title, and colorized salary to salary_dict
                salary_dict['url'] = url
                salary_dict['title'] = job_title
                salary_dict['salary'] = colorize_salary(salary_dict['salary']) if salary_dict['salary'] else 'No Data'
                salaries.append(salary_dict)
            else:
                # Add company name, job title, and None salary to salaries list
                salaries.append({'company': company_name, 'title': job_title, 'salary': None, 'url': url})
                        

        # do stuff if `-t` is passed 
        if args.table:
            # Remove entries with no salary data
            salaries = [s for s in salaries if s['salary'] is not None]

            # Sort the salaries list based on the median salary
            salaries = sorted(salaries, key=lambda x: int(re.sub(r'\x1b\[\d+m|\$', '', x['salary']).replace(',', '')) if isinstance(x['salary'], str) else x['salary'], reverse=True)

            # Print the table header
            print("\033[1m{:<16} {:<16} {:<50} {:<50}\033[0m".format("Company Name", "Median Salary", "Job Title", "Job URL"))

            # Print each row in the table
            for salary in salaries:
                print("{:<25} {:<25} {:<50} {:<50}".format("\033[35m" + salary['company'] + "\033[0m", salary['salary'], salary['title'], salary['url']))


        # do stuff if `-t` isn't passed 
        else:
            for url_dict, salary in zip(job_urls, salaries):
                url = url_dict['url']
                job_title = url_dict['title']
                print(f"Job URL: {url}")
                print(f"Job Title: {job_title}")
                print(f"Company: \033[35m{salary['company']}\033[0m")
                if salary['salary'] is None:
                    print("No salary information found for this company.")
                else:
                    median_salary = salary['salary']
                    print(f"Median Total Comp for Software Engineer: {colorize_salary(median_salary)}")
                print("-" * 50)


    # do stuff if `-c` is passed 
    if args.company:
        median_salary = get_salary(args.company, use_decimal=args.table)
        print(f"Company: \033[35m{median_salary['company']}\033[0m")
        if median_salary['salary'] is None:
            print("No salary information found for this company.")
        else:
            median_salary = median_salary['salary']
            if args.table:
                print(f"Median Total Comp for Software Engineer: {colorize_salary(median_salary)}")
            else:
                print(f"Median Total Comp for Software Engineer: {colorize_salary(int(median_salary))}")
        print("-" * 50)


if __name__ == "__main__":
    main()

