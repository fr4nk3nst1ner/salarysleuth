import argparse
from argparse import RawTextHelpFormatter, SUPPRESS
import re
import subprocess
import requests
from bs4 import BeautifulSoup


BANNER = """
\033[38;5;196m$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
$$$                                                                          $$$
$$$                               SALARY SLEUTH                              $$$
$$$                               @fr4nk3nst1ner                             $$$
$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
\033[0m
"""


def get_job_urls(query):
    # Define the go-dork command
    godork_cmd = f'go-dork -e google -p 50 -s -q "{query}"'

    # Run the go-dork command and capture the output
    output = subprocess.check_output(godork_cmd, shell=True)

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
    salary_elem = soup.find("td", text="Software Engineer Salary")
    if salary_elem:
        salary = salary_elem.find_next_sibling("td").text
        return salary

    return "No Entries on levels.fyi :("


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

def main():
    parser = argparse.ArgumentParser(description="Examples: \n python salarysleuth.py -j kali \n python salarysleuth.py -j oscp \n python salarysleuth.py -c rapid7 \n python salarysleuth.py -c salesforce", formatter_class=RawTextHelpFormatter,usage=SUPPRESS)
    parser.add_argument("-j", "--job", type=str, help="Job characteristic to search for on job listing websites")
    parser.add_argument("-c", "--company", type=str, help="Name of a specific company to search for salary information")
    parser.add_argument("-s", "--silence", action="store_true", help="Silence the banner")

    args = parser.parse_args()

    if not args.job and not args.company:
        print("Please provide a job title or company name to search for. Use --help for usage details.")
        return

    print_banner(args.silence)


    if args.job:
        dork_query = f"site:lever.co OR site:greenhouse.io {args.job}"
        job_urls = get_job_urls(dork_query)

        # uncomment these lines if you want unique companeies only 
        #seen_companies = set()
        for url in job_urls:
            company_name = get_company_name(url)
        #    if company_name in seen_companies:
        #        continue
        #    else:
        #        seen_companies.add(company_name)

            median_salary = get_salary(company_name)

            print(f"Job URL: {url}")
            print(f"Company: \033[35m{company_name}\033[0m")
            if median_salary == "No Entries on levels.fyi :(":
                print(f"Median Total Comp for Software Engineer: {median_salary}")
            else:
                median_salary = int(median_salary.replace(",", "").replace("$", ""))
                if median_salary >= 300000:
                    print(f"Median Total Comp for Software Engineer: \033[32m{median_salary:,}\033[0m")
                elif median_salary >= 200000:
                    print(f"Median Total Comp for Software Engineer: \033[92m{median_salary:,}\033[0m")
                elif median_salary >= 100000:
                    print(f"Median Total Comp for Software Engineer: \033[93m{median_salary:,}\033[0m")
                else:
                    print(f"Median Total Comp for Software Engineer: \033[31m{median_salary:,}\033[0m")
            print("-" * 50)

    if args.company:
        median_salary = get_salary(args.company)
        print(f"Company: \033[35m{args.company}\033[0m")
        if median_salary == "No Entries on levels.fyi :(":
            print(f"Median Salary for Software Engineer: {median_salary}")
        else:
            median_salary = int(median_salary.replace(",", "").replace("$", ""))
            if median_salary >= 300000:
                print(f"Median Total Comp for Software Engineer: \033[32m{median_salary:,}\033[0m")
            elif median_salary >= 200000:
                print(f"Median Total Comp for Software Engineer: \033[92m{median_salary:,}\033[0m")
            elif median_salary >= 100000:
                print(f"Median Total Comp for Software Engineer: \033[93m{median_salary:,}\033[0m")
            else:
                print(f"Median Total Comp for Software Engineer: \033[31m{median_salary:,}\033[0m")
        print("-" * 50)


if __name__ == "__main__":
    main()

