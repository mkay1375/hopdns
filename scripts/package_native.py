"""Package one executable with its legal notices and a SHA-256 checksum."""
import argparse
import hashlib
from pathlib import Path
import shutil
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--target', required=True)
    parser.add_argument('--go-license', type=Path, required=True)
    args = parser.parse_args()
    binary = args.binary.resolve()
    name = 'hopdns-' + args.target
    output = binary.parent
    with tempfile.TemporaryDirectory(prefix='hopdns-package-') as temp:
        directory = Path(temp) / name
        directory.mkdir()
        shutil.copy2(binary, directory / binary.name)
        for notice in ('LICENSE', 'THIRD_PARTY_NOTICES.md'):
            shutil.copy2(ROOT / notice, directory / notice)
        shutil.copytree(ROOT / 'docs/licenses', directory / 'licenses')
        shutil.copy2(args.go_license, directory / 'licenses/go-runtime.txt')
        if args.target.startswith('windows-'):
            archive = output / (name + '.zip')
            with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as bundle:
                for path in sorted(directory.rglob('*')):
                    if path.is_file():
                        bundle.write(path, path.relative_to(directory.parent))
        else:
            archive = output / (name + '.tar.gz')
            with tarfile.open(archive, 'w:gz') as bundle:
                bundle.add(directory, arcname=name)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    (output / (name + '-SHA256SUMS')).write_text(digest + '  ' + archive.name + '\n')
    print(archive)


if __name__ == '__main__':
    main()
