Name:           dmcode
Version:        %{version}
Release:        1%{?dist}
Summary:        Terminal coding agent built on google/adk-go
License:        MIT
URL:            https://github.com/dedomorozoff/dmcode
BuildArch:      x86_64

%description
dmcode is a terminal-based AI coding assistant with a Bubble Tea TUI,
provider failover and file/shell tools.

%prep

%build

%install
mkdir -p %{buildroot}%{_bindir}
# rpmbuild runs this section with the cwd set to the buildroot, not the
# checkout, so the prebuilt binary has to be named by an absolute path. The
# Makefile passes the checkout location in as srcdir.
cp -f %{srcdir}/dist/dmcode-linux-amd64 %{buildroot}%{_bindir}/dmcode

%files
%{_bindir}/dmcode

%changelog
* Mon Jan 01 2024 dmcode <noreply@github.com> - %{version}-1
- Initial package
