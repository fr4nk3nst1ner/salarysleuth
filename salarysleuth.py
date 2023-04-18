#!/usr/local/bin/python3
import argparse
from argparse import RawTextHelpFormatter, SUPPRESS
import re
import subprocess
import requests
from bs4 import BeautifulSoup


BANNER = """
\033[38;5;196m$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
$$$                                                      $$$
$$$                     $alary $leuth                    $$$
$$$                     @fr4nk3nst1ner                   $$$
$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
\033[0m
"""


def get_job_urls(query, num_pages, search_engine):
    # Define the go-dork command
    godork_cmd = f'go-dork -e {search_engine} -p {num_pages} -s -q "{query}"'

    # Run the go-dork command and capture the output
    output = subprocess.check_output(godork_cmd, shell=True, stderr=subprocess.DEVNULL)

    # Decode the output as a string
    output_str = output.decode('utf-8')

    # Extract the URLs from the output using regular expressions
    job_urls = re.findall(r'https?://\S+', output_str)

    return job_urls


def get_salary(company_name):
    url = f"https://www.levels.fyi/companies/{company_name}/salaries/"
    response = requests.get(url)
    html = response.content.decode()

    # Extract salary info using BeautifulSoup
    soup = BeautifulSoup(html, "html.parser")
    salary_elem = soup.find("td", string="Software Engineer Salary")
    if salary_elem:
        salary = salary_elem.find_next_sibling("td").text
        salary = int(salary.replace(",", "").replace("$", ""))
        salary = float(salary) if salary else None
        return {'company': company_name, 'salary': salary}

    return None
    #return "No Entries on levels.fyi :("


def get_company_name(url):
    # Extract the company name from the URL
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
        return "No Data"
    elif salary >= 300000:
        return f"\033[32m${salary:,.0f}\033[0m"
    elif salary >= 200000:
        return f"\033[92m${salary:,.0f}\033[0m"
    elif salary >= 100000:
        return f"\033[93m${salary:,.0f}\033[0m"
    else:
        return f"\033[31m${salary:,.0f}\033[0m"



def main():
    parser = argparse.ArgumentParser(description="Examples: \n python salarysleuth.py -j kali \n python salarysleuth.py -j oscp \n python salarysleuth.py -c rapid7 \n python salarysleuth.py -c salesforce", formatter_class=RawTextHelpFormatter,usage=SUPPRESS)
    parser.add_argument("-j", "--job", type=str, help="Job characteristic to search for on job listing websites")
    parser.add_argument("-c", "--company", type=str, help="Name of a specific company to search for salary information")
    parser.add_argument("-s", "--silence", action="store_true", help="Silence the banner")
    parser.add_argument("-p", "--pages", type=int, default=50, help="Number of search result pages to scrape (default: 50)")
    parser.add_argument("-e", "--engine", type=str, default='google', help="Search engine to use (default: google). \n Options: Google, Shodan, Bing, Duck, Yahoo, Ask \n Note: Only tested with Google")
    parser.add_argument("-t", "--table", action="store_true", help="Re-organize output into a table in ascending order based on median salary")

    args = parser.parse_args()

    if not args.job and not args.company:
        print("Please provide a job title or company name to search for. Use --help for usage details.")
        return

    print_banner(args.silence)


    if args.job:
        dork_query = f"site:lever.co OR site:greenhouse.io {args.job}"
        job_urls = get_job_urls(dork_query, args.pages, args.engine)

        salaries = []
        for url in job_urls:
            company_name = get_company_name(url)
            salary_dict = get_salary(company_name)
            if salary_dict is not None:
                salary_dict['url'] = url  # Add URL to salary_dict
                salaries.append(salary_dict)

        if args.table:
            # Sort the salaries list based on the median salary
            salaries = sorted(salaries, key=lambda x: x['salary'], reverse=True)

            # Print the table header
            print("\033[1m{:<16} {:<16} {:<50}\033[0m".format("Company Name", "Median Salary", "Job URL"))


            # Print each row in the table
            for salary in salaries:
                print("{:<25} {:<25} {:<50}".format("\033[35m" + salary['company'] + "\033[0m", colorize_salary(salary['salary']), salary['url']))

        else:
            for salary in salaries:
                print(f"Job URL: {url}")
                print(f"Company: \033[35m{salary['company']}\033[0m")
                if salary['salary'] is None:
                    print("No salary information found for this company.")
                else:
                    median_salary = salary['salary']
                    if median_salary >= 300000:
                        print(f"Median Total Comp for Software Engineer: \033[32m${median_salary:,}\033[0m")
                    elif median_salary >= 200000:
                        print(f"Median Total Comp for Software Engineer: \033[92m${median_salary:,}\033[0m")
                    elif median_salary >= 100000:
                        print(f"Median Total Comp for Software Engineer: \033[93m${median_salary:,}\033[0m")
                    else:
                        print(f"Median Total Comp for Software Engineer: \033[31m${median_salary:,}\033[0m")
                print("-" * 50)

    if args.company:
        median_salary = get_salary(args.company)
        print(f"Company: \033[35m{median_salary['company']}\033[0m")
        if median_salary['salary'] is None:
            print("No salary information found for this company.")
        else:
            median_salary = median_salary['salary']
            if median_salary >= 300000:
                print(f"Median Total Comp for Software Engineer: \033[32m${median_salary:,}\033[0m")
            elif median_salary >= 200000:
                print(f"Median Total Comp for Software Engineer: \033[92m${median_salary:,}\033[0m")
            elif median_salary >= 100000:
                print(f"Median Total Comp for Software Engineer: \033[93m${median_salary:,}\033[0m")
            else:
                print(f"Median Total Comp for Software Engineer: \033[31m${median_salary:,}\033[0m")
        print("-" * 50)


if __name__ == "__main__":
    main()

