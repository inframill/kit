"""Install tools and accompanying sources using runtime/dependencies.json pins."""
import hashlib
import json
import os
import pathlib
import shlex
import subprocess
import sys
import tempfile
import urllib.request
import zipfile


def dependencies(path=None):
    return json.loads(pathlib.Path(path or pathlib.Path(__file__).with_name('dependencies.json')).read_text())


def ansible_requirements(settings):
    return ['ansible-core==' + settings['ANSIBLE_VERSION'], *shlex.split(settings['ANSIBLE_DEPENDENCIES'])]


def install_ansible(python, settings, output, report, *, env=None, cwd=None):
    """Use the same installation contract for native setup and Docker."""
    output = pathlib.Path(output)
    subprocess.run([str(python), '-m', 'venv', str(output)], check=True, env=env, cwd=cwd, timeout=120)
    pip = str(output / 'bin/pip')
    subprocess.run([pip, 'install', '--no-cache-dir', '--report', str(report),
                    *ansible_requirements(settings)], check=True, env=env, cwd=cwd, timeout=900)
    subprocess.run([pip, 'check'], check=True, env=env, cwd=cwd, timeout=120)


def download(url, checksum, destination):
    destination = pathlib.Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    hasher = hashlib.sha256()
    with urllib.request.urlopen(url, timeout=120) as response, destination.open('wb') as stream:
        for chunk in iter(lambda: response.read(1 << 20), b''):
            hasher.update(chunk)
            stream.write(chunk)
    if hasher.hexdigest() != checksum:
        destination.unlink()
        raise ValueError('Checksum mismatch for ' + url)
    return destination


def install_opentofu(target, settings, output):
    checksum = settings['OPENTOFU_SHA256_' + target.replace('/', '_').upper()]
    version = settings['OPENTOFU_VERSION']
    url = f'https://github.com/opentofu/opentofu/releases/download/v{version}/tofu_{version}_{target.replace("/", "_")}.zip'
    output = pathlib.Path(output)
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='kit-tofu-') as td:
        archive_path = download(url, checksum, pathlib.Path(td) / 'tofu.zip')
        with zipfile.ZipFile(archive_path) as archive:
            (output / 'tofu').write_bytes(archive.read('tofu'))
            for name in archive.namelist():
                if 'LICENSE' in name.upper() and '/' not in name:
                    (output / name).write_bytes(archive.read(name))
    (output / 'tofu').chmod(0o755)


def fetch_sources(settings, report, output):
    """Include sources for the exact packages installed in this image."""
    output = pathlib.Path(output)
    version = settings['OPENTOFU_VERSION']
    download(f'https://codeload.github.com/opentofu/opentofu/tar.gz/refs/tags/v{version}',
             settings['OPENTOFU_SOURCE_SHA256'], output / f'opentofu-{version}.tar.gz')
    sources = []
    for package in json.loads(pathlib.Path(report).read_text())['install']:
        name, version = package['metadata']['name'], package['metadata']['version']
        with urllib.request.urlopen(f'https://pypi.org/pypi/{name}/{version}/json', timeout=60) as response:
            metadata = json.load(response)
        source = next(entry for entry in metadata['urls'] if entry['packagetype'] == 'sdist')
        download(source['url'], source['digests']['sha256'], output / source['filename'])
        sources.append({'name': name, 'version': version, 'url': source['url'], 'sha256': source['digests']['sha256']})
    (output / 'python-sources.json').write_text(json.dumps(sources, indent=2) + '\n')


if __name__ == '__main__':
    if sys.argv[1] == 'ansible':
        settings = dependencies()
        install_ansible(sys.executable, settings, '/opt/inframill/tools/ansible/' + settings['ANSIBLE_VERSION'],
                        '/usr/share/inframill/python-packages.json')
    elif sys.argv[1] == 'opentofu':
        install_opentofu('linux/' + os.environ['TARGETARCH'], dependencies(),
                         '/opt/inframill/tools/opentofu/' + dependencies()['OPENTOFU_VERSION'])
    elif sys.argv[1] == 'sources':
        fetch_sources(dependencies(), '/usr/share/inframill/python-packages.json', '/usr/share/inframill/sources')
    else:
        sys.exit('Expected ansible, opentofu or sources')
