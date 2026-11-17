# Avoid Using AWS Credentials on Your Laptop

With Chekov and devcontainers in play, there should be no reason to use aws credentials on your laptop host.

To avoid accidentally creating or using AWS credentials on your laptop, follow these guidelines:

- **Do not run `aws configure` on your host laptop.** Run it only inside a devcontainer or Docker container.
- Do not store AWS credentials or configuration files in your host's `~/.aws` directory.
- Ensure AWS CLI commands requiring credentials are executed within the appropriate development container.
- If you need to use AWS CLI on your laptop, ensure you understand which credentials and configuration it will use.

## Optional safeguard 1: Block `aws configure`

Add the following function to your host shell configuration (`~/.zshrc` or `~/.bashrc`) to prevent accidental use of `aws configure`:

```bash
aws() {
    case "$1" in
        configure)
            echo "ERROR: aws configure is disabled on the host."
            echo "Run it inside a devcontainer instead."
            return 1
            ;;
    esac

    command aws "$@"
}
```

Reload your shell configuration after adding the function.

## Optional safeguard 2: Make `~/.aws` immutable (macOS)

Create the directory and mark it as immutable:

```bash
mkdir -p ~/.aws
chflags uchg ~/.aws
```

This prevents normal processes from creating or modifying files inside `~/.aws`.

To remove the protection when necessary:

```bash
chflags nouchg ~/.aws
```

**Note:** This is a macOS-specific safeguard. It can also prevent legitimate AWS tooling from writing configuration or credentials to this directory.